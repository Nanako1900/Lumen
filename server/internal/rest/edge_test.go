package rest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lumen/internal/store"
)

// contextTODO is a small alias for readability in tests.
func contextTODO() context.Context { return context.Background() }

// testLogger returns a discard slog logger for middleware tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardW{}, nil))
}

type discardW struct{}

func (discardW) Write(p []byte) (int, error) { return len(p), nil }

// remarshalBody decodes a recorder's JSON body into dst.
func remarshalBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func TestParseLimit(t *testing.T) {
	cases := map[string]int{
		"":     store.DefaultMessageLimit,
		"abc":  store.DefaultMessageLimit,
		"0":    1,
		"-5":   1,
		"50":   50,
		"1000": store.MaxMessageLimit,
		"100":  100,
	}
	for in, want := range cases {
		if got := parseLimit(in); got != want {
			t.Errorf("parseLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestWSURL_DerivedFromHost(t *testing.T) {
	e := newTestEnv(t)
	_ = e.st.SeedDefaultChannels(contextTODO())
	token := e.signer.Token(t, "sub-1", "A", "")

	// Without config, wsURL derives from Host header.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	r.Host = "chat.example.com"
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, r)

	var env Envelope
	remarshalBody(t, rec, &env)
	var resp bootstrapResp
	remarshal(t, env.Data, &resp)
	if resp.WSURL != "wss://chat.example.com/ws" {
		t.Errorf("ws_url = %q, want wss://chat.example.com/ws (from Host)", resp.WSURL)
	}
}

func TestMe_UpsertError(t *testing.T) {
	e := newTestEnv(t)
	e.st.UpsertHook = func(string) error { return errors.New("db down") }
	token := e.signer.Token(t, "sub-1", "A", "")
	code, env := e.do(t, http.MethodGet, "/api/v1/me", token, "")
	if code != http.StatusInternalServerError || env.Error == nil || env.Error.Code != "INTERNAL" {
		t.Errorf("upsert error: code=%d err=%+v, want 500 INTERNAL", code, env.Error)
	}
}

func TestBootstrap_UpsertError(t *testing.T) {
	e := newTestEnv(t)
	e.st.UpsertHook = func(string) error { return errors.New("db down") }
	token := e.signer.Token(t, "sub-1", "A", "")
	code, env := e.do(t, http.MethodGet, "/api/v1/bootstrap", token, "")
	if code != http.StatusInternalServerError || env.Error.Code != "INTERNAL" {
		t.Errorf("bootstrap upsert error: code=%d err=%+v, want 500 INTERNAL", code, env.Error)
	}
}

func TestWithRecover_CatchesPanic(t *testing.T) {
	panicky := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	h := withRecover(testLogger(), panicky)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	var env Envelope
	remarshalBody(t, rec, &env)
	if env.Error == nil || env.Error.Code != "INTERNAL" {
		t.Errorf("recovered error = %+v, want INTERNAL", env.Error)
	}
}

func TestMessagesInlineAuthor(t *testing.T) {
	e := newTestEnv(t)
	e.st.AddChannel(store.Channel{ID: "t1", Name: "general", Type: "text"})
	e.st.AddUser(store.User{ID: "author-1", OAuthSubject: "sub-author", DisplayName: "Writer"})
	if _, err := e.st.InsertMessage(contextTODO(), "t1", "author-1", "hi"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	token := e.signer.Token(t, "sub-1", "A", "")

	code, env := e.do(t, http.MethodGet, "/api/v1/channels/t1/messages", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var resp messagesResp
	remarshal(t, env.Data, &resp)
	if len(resp.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(resp.Messages))
	}
	if resp.Messages[0].Author == nil || resp.Messages[0].Author.DisplayName != "Writer" {
		t.Errorf("author snapshot = %+v, want Writer inlined", resp.Messages[0].Author)
	}
	// Empty channel -> next_before nil, has_more false.
	e.st.AddChannel(store.Channel{ID: "empty", Name: "e", Type: "text"})
	_, env = e.do(t, http.MethodGet, "/api/v1/channels/empty/messages", token, "")
	remarshal(t, env.Data, &resp)
	if resp.Meta.NextBefore != nil || resp.Meta.HasMore {
		t.Errorf("empty channel meta = %+v, want nil next_before, has_more false", resp.Meta)
	}
}
