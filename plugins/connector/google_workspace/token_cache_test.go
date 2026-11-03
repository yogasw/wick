package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// fakeGoogle answers the token endpoint with a new access token per refresh
// and the API with 200 only for the token it considers valid.
type fakeGoogle struct {
	refreshes atomic.Int32
	valid     atomic.Value // string
}

func (f *fakeGoogle) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(code int, body string) *http.Response {
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}
	}
	if r.URL.Host == "oauth2.googleapis.com" {
		n := f.refreshes.Add(1)
		tok := fmt.Sprintf("fresh-%d", n)
		f.valid.Store(tok)
		return reply(200, fmt.Sprintf(`{"access_token":%q,"expires_in":3599}`, tok)), nil
	}
	auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case auth == "":
		return reply(403, `{"error":{"message":"Method doesn't allow unregistered callers"}}`), nil
	case auth != f.valid.Load():
		return reply(401, `{"error":{"code":401}}`), nil
	}
	return reply(200, `{"ok":true}`), nil
}

func newTestCtx(t *testing.T, g *fakeGoogle, cfg map[string]string) *connector.Ctx {
	t.Helper()
	return connector.NewCtx(t.Context(), "test", cfg, nil, &http.Client{Transport: g}, nil, nil)
}

func callAPI(c *connector.Ctx) error {
	_, err := doWithRefresh(c, func(token string) (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, "https://www.googleapis.com/drive/v3/files", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return req, nil
	})
	return err
}

func TestDoWithRefresh_EmptyAccessTokenRefreshesOnceThenCaches(t *testing.T) {
	g := &fakeGoogle{}
	g.valid.Store("")
	cfg := map[string]string{"client_id": "cid-empty", "refresh_token": "rt-empty"}

	for i := 0; i < 3; i++ {
		if err := callAPI(newTestCtx(t, g, cfg)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := g.refreshes.Load(); n != 1 {
		t.Fatalf("refreshes = %d, want 1 (later calls reuse the cached token)", n)
	}
}

func TestDoWithRefresh_RejectedCachedTokenIsReplaced(t *testing.T) {
	g := &fakeGoogle{}
	g.valid.Store("")
	cfg := map[string]string{"client_id": "cid-revoke", "refresh_token": "rt-revoke"}

	if err := callAPI(newTestCtx(t, g, cfg)); err != nil {
		t.Fatal(err)
	}
	g.valid.Store("something-else") // Google stops accepting the cached token
	if err := callAPI(newTestCtx(t, g, cfg)); err != nil {
		t.Fatalf("after revoke: %v", err)
	}
	if n := g.refreshes.Load(); n != 2 {
		t.Fatalf("refreshes = %d, want 2", n)
	}
}

func TestDoWithRefresh_StoredTokenWithoutRefreshTokenUnchanged(t *testing.T) {
	g := &fakeGoogle{}
	g.valid.Store("stored")
	ok := map[string]string{"client_id": "cid-plain", "user_token": "stored"}
	if err := callAPI(newTestCtx(t, g, ok)); err != nil {
		t.Fatal(err)
	}
	expired := map[string]string{"client_id": "cid-plain", "user_token": "old"}
	if err := callAPI(newTestCtx(t, g, expired)); err == nil || !strings.Contains(err.Error(), "Reconnect") {
		t.Fatalf("err = %v, want a reconnect hint", err)
	}
	if n := g.refreshes.Load(); n != 0 {
		t.Fatalf("refreshes = %d, want 0 without a refresh token", n)
	}
}

func TestProbeTokenScopes_UsesCachedToken(t *testing.T) {
	g := &fakeGoogle{}
	g.valid.Store("")
	cfg := map[string]string{"client_id": "cid-probe", "refresh_token": "rt-probe"}
	if err := callAPI(newTestCtx(t, g, cfg)); err != nil {
		t.Fatal(err)
	}
	if got := cachedAccessToken(newTestCtx(t, g, cfg)); got != g.valid.Load() {
		t.Fatalf("cached = %q, want %q", got, g.valid.Load())
	}
	forgetAccessToken(newTestCtx(t, g, cfg))
	if got := cachedAccessToken(newTestCtx(t, g, cfg)); got != "" {
		t.Fatalf("cached after forget = %q, want empty", got)
	}
}
