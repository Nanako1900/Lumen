package signaling

import (
	"context"
	"errors"
	"time"

	"lumen/internal/auth"
	"lumen/internal/protocol"
)

// errClose signals the read side to tear the connection down.
var errClose = errors.New("close connection")

// serve runs the full connection lifecycle: handshake, then the message loop.
// It always cleans up on return.
func (c *Client) serve(ctx context.Context) {
	defer c.closeConn()

	// Write pump runs for the whole connection lifetime.
	go c.writeLoop()

	if err := c.handshake(ctx); err != nil {
		return
	}
	c.readLoop(ctx)
}

// handshake performs the auth first-frame handshake within handshakeTimeout
// (contract §2.5). Only an auth frame is accepted before authentication; any
// other type is rejected with auth_error and the connection closes.
func (c *Client) handshake(ctx context.Context) error {
	// The read runs in its own goroutine so a handshake timeout does not depend
	// on the read context cancelling the connection (coder/websocket fails the
	// whole conn when a Read ctx expires, which would prevent us from writing
	// the timeout auth_error on the still-open socket).
	type readResult struct {
		env protocol.Envelope
		err error
	}
	resultCh := make(chan readResult, 1)
	go func() {
		env, err := c.readEnvelope(ctx)
		resultCh <- readResult{env: env, err: err}
	}()

	timer := time.NewTimer(handshakeTimeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		c.sendNow(authError("HANDSHAKE_TIMEOUT", "握手超时"))
		return errClose
	case res := <-resultCh:
		if res.err != nil {
			c.sendNow(authError("TOKEN_INVALID", "需先鉴权"))
			return errClose
		}
		if res.env.Type != "auth" {
			c.sendNow(authError("TOKEN_INVALID", "需先鉴权"))
			return errClose
		}
		return c.applyAuth(ctx, res.env, false)
	}
}

// applyAuth handles both auth (first frame) and reauth (contract §2.5/§2.6/§2.7).
// On success it binds the session, registers the client (first auth only), and
// replies auth_ok; when a profile change is detected it broadcasts user_updated.
func (c *Client) applyAuth(ctx context.Context, env protocol.Envelope, isReauth bool) error {
	var d protocol.AuthData
	if err := decodeData(env.Data, &d); err != nil || d.AccessToken == "" {
		c.sendNow(authError("TOKEN_INVALID", "缺少访问令牌"))
		return errClose
	}

	claims, err := c.hub.verifier.Verify(d.AccessToken)
	if err != nil {
		if isReauth {
			// reauth failure does not immediately close (30s window, [v1]); but
			// [v0] has no mid-connection reauth window, so treat as close.
			code := "TOKEN_INVALID"
			if auth.IsExpired(err) {
				code = "TOKEN_EXPIRED"
			}
			c.enqueue(wsError(code, "令牌校验失败", env.ID))
			return errClose
		}
		code := "TOKEN_INVALID"
		if auth.IsExpired(err) {
			code = "TOKEN_EXPIRED"
		}
		c.sendNow(authError(code, "令牌校验失败"))
		return errClose
	}

	// Soft-ban check (auth and reauth, loop-5): reject during the cooldown so a
	// stale connection cannot dodge the ban via reauth.
	if u, err := c.hub.store.GetUserBySubject(ctx, claims.Subject); err == nil {
		if u.KickedUntil != nil && u.KickedUntil.After(time.Now()) {
			c.sendNow(c.hub.kickedAuthError(u.ID, "KICKED"))
			return errClose
		}
	}

	// Profile sync: map claims, optionally enrich via userinfo, then upsert.
	profile := auth.ProfileFromClaims(claims)
	if c.hub.enricher != nil {
		profile = c.hub.enricher.Enrich(ctx, d.AccessToken, profile)
	}
	user, changed, err := c.hub.store.UpsertUser(ctx, profile.Subject, profile.DisplayName, profile.AvatarURL)
	if err != nil {
		c.sendNow(authError("TOKEN_INVALID", "用户初始化失败"))
		return errClose
	}

	// Bind session state.
	c.subject = claims.Subject
	c.isOwner = c.hub.owners.IsOwner(claims.Subject)
	if claims.ExpiresAt != nil {
		c.tokenExp.Store(claims.ExpiresAt.Unix())
	}
	if !isReauth {
		c.userID = user.ID
		c.authed.Store(true)
		c.hub.register(c)
	}

	userDTO := auth.ToDTO(user, c.hub.owners)
	c.enqueue(protocol.NewEnvelope("auth_ok", protocol.AuthOKData{
		User:       userDTO,
		ServerTime: time.Now().UTC().Format(time.RFC3339),
		Reauth:     isReauth,
	}))

	// Broadcast user_updated only when an existing row actually changed
	// (sync-3): first-time INSERT never broadcasts. [v1] consumers ignore it.
	if changed {
		c.hub.BroadcastAll(protocol.NewEnvelope("user_updated", userDTO))
	}
	c.hub.logger.Info("ws auth", "reauth", isReauth, "sub_prefix", subPrefix(claims.Subject))
	return nil
}

// readLoop dispatches inbound messages after authentication until the
// connection closes.
func (c *Client) readLoop(ctx context.Context) {
	for {
		select {
		case <-c.closed:
			return
		case <-ctx.Done():
			return
		default:
		}
		env, err := c.readEnvelope(ctx)
		if err != nil {
			return
		}
		c.dispatch(ctx, env)
	}
}

// authError builds an auth_error envelope (fatal; connection closes after).
func authError(code, message string) protocol.Envelope {
	return protocol.NewEnvelope("auth_error", protocol.AuthErrorData{Code: code, Message: message})
}

// subPrefix returns a short, de-identified subject prefix for logs.
func subPrefix(sub string) string {
	if len(sub) <= 6 {
		return sub
	}
	return sub[:6]
}
