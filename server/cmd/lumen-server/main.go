// Command lumen-server is the Lumen backend entrypoint (server-design §7.2). It
// loads configuration (fail-fast), opens the PostgreSQL pool, runs the schema
// migration and default-channel seed, builds the auth verifier / owner set /
// profile enricher, wires the SFU + signaling hub, mounts the REST + account
// center/desktop broker routes behind a single CORS middleware, starts the
// broker janitor, and serves HTTP with graceful shutdown on SIGINT/SIGTERM.
//
// This is the Docker build target: `go build ./cmd/lumen-server`.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"lumen/internal/auth"
	"lumen/internal/broker"
	"lumen/internal/config"
	"lumen/internal/rest"
	"lumen/internal/secure"
	"lumen/internal/sfu"
	"lumen/internal/signaling"
	"lumen/internal/store"
)

const (
	// janitorInterval is how often the broker janitor reclaims expired
	// login_ctx / handoff rows (decision 4).
	janitorInterval = 60 * time.Second
	// shutdownTimeout bounds the graceful HTTP drain on SIGINT/SIGTERM.
	shutdownTimeout = 15 * time.Second
	// startupTimeout bounds the one-shot startup work (DB open, migrate, seed,
	// discovery) so a hung dependency fails fast instead of blocking forever.
	startupTimeout = 30 * time.Second
	// readHeaderTimeout guards against slow-loris on the HTTP listener.
	readHeaderTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// run performs the full wiring and blocks until shutdown. Returning an error
// (rather than calling os.Exit inside) keeps the flow testable and lets deferred
// cleanups run.
func run() error {
	// 1) Config (fail-fast): a missing/invalid key aborts before any I/O.
	cfg, err := config.Load()
	if err != nil {
		// Bootstrap logger only; the real logger's level comes from cfg below.
		slog.Error("加载配置失败", "err", err)
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	// rootCtx is cancelled on SIGINT/SIGTERM; it bounds background goroutines
	// (JWKS refresh, broker janitor) and triggers graceful shutdown.
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 2) Database pool + schema + seed (bounded by a startup context).
	startCtx, cancelStart := context.WithTimeout(rootCtx, startupTimeout)
	defer cancelStart()

	db, err := store.Open(startCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	// The refresh-token at-rest sealer uses the SEPARATE LUMEN_REFRESH_ENC_KEY
	// (decision 2); the store encrypts desktop_sessions.refresh_token_enc with it.
	refreshSealer, err := secure.NewSealer(cfg.RefreshEncKey())
	if err != nil {
		return err
	}
	st := store.NewWithSealer(db, refreshSealer)

	if err := st.Migrate(startCtx); err != nil {
		return err
	}
	if err := st.SeedDefaultChannels(startCtx); err != nil {
		return err
	}

	// 3) Auth: JWKS verifier (background refresh bound to rootCtx), owner set,
	// and the optional userinfo profile enricher (claims-only on discovery fail).
	verifier, err := auth.NewVerifier(rootCtx, cfg.OAuthJWKSURL, cfg.OAuthIssuer, cfg.OAuthAudience)
	if err != nil {
		return err
	}
	owners := auth.NewOwnerSet(cfg.OwnerSubjects)

	enricher, err := auth.NewProfileEnricher(startCtx, cfg.OAuthIssuer, cfg.OAuthUserinfoURL)
	if err != nil {
		// Degrade gracefully to claims-only rather than refusing to boot.
		logger.Warn("userinfo enricher 初始化失败，降级为仅凭 claims", "err", err)
		enricher = nil
	}

	// 4) SFU + signaling hub. The RoomManager and Hub are cross-wired via
	// SetSink (breaks the sfu↔signaling import cycle).
	api, err := sfu.NewAPI(cfg.WebRTCUDPPort, cfg.PublicIP)
	if err != nil {
		return err
	}
	rooms := sfu.NewRoomManager(api)
	hub := signaling.NewHub(st, verifier, owners, rooms, enricher, logger)
	rooms.SetSink(hub)

	// 5) Account center / desktop broker (decision 10). Discovery fallback for
	// authorize/token/userinfo URLs happens inside NewHandler when they are
	// empty; the session-cookie sealer uses LUMEN_SESSION_ENC_KEY.
	brk, err := broker.NewHandler(startCtx, cfg, st, logger)
	if err != nil {
		return err
	}

	// 6) Router: REST (+ broker) behind a single CORS middleware, then the
	// signaling Mount wraps it so GET /ws upgrades and everything else falls
	// through to the CORS-wrapped mux.
	router := rest.NewRouter(rest.Deps{
		Verifier: verifier,
		Owners:   owners,
		Enricher: enricher,
		Store:    st,
		Rooms:    rooms,
		Hub:      hub,
		Config:   cfg,
		Logger:   logger,
		Broker:   brk,
	})
	handler := signaling.Mount(router, hub)

	// 7) Broker janitor: reclaim expired broker_states every 60s until shutdown.
	go runJanitor(rootCtx, st, logger)

	// 8) HTTP server with graceful shutdown.
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening",
			"addr", cfg.ListenAddr,
			"udp", cfg.WebRTCUDPPort,
			"log_level", cfg.LogLevel,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	// Block until a signal cancels rootCtx or the listener fails.
	select {
	case <-rootCtx.Done():
		logger.Info("shutdown signal received, draining")
	case err := <-serveErr:
		if err != nil {
			return err
		}
		return nil
	}

	// Graceful drain: stop accepting, finish in-flight requests, close voice PCs.
	shutCtx, cancelShut := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShut()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		rooms.CloseAllRooms()
		return err
	}
	rooms.CloseAllRooms()
	logger.Info("shutdown complete")
	return nil
}

// runJanitor runs DeleteExpiredBrokerStates on a 60s ticker until ctx is done
// (decision 4). Errors are logged and the loop continues; a single failed sweep
// is harmless because every read already guards WHERE expires_at > now().
func runJanitor(ctx context.Context, st store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := st.DeleteExpiredBrokerStates(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				logger.Error("broker janitor sweep failed", "err", err)
				continue
			}
			if n > 0 {
				logger.Debug("broker janitor swept expired states", "removed", n)
			}
		}
	}
}

// newLogger builds a structured slog JSON logger at the configured level. An
// unknown level falls back to info. Secrets are never logged; the config layer
// keeps the enc keys and client_secret out of any logged value.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
