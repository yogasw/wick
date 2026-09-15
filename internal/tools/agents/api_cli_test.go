package agents

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/clitoken"
)

// The CLI surface is deliberately tiny. Anything outside it must not be
// reachable with a CLI token, and the ticket surface must not be either:
// two validators share one mount, and the whole point is that neither
// widens the other.
func TestCLIAPIPathAllowlist(t *testing.T) {
	for _, ok := range []string{
		"/tools/agents/api/cli/send",
		"/tools/agents/api/cli/whoami",
	} {
		if !isCLIAPIPath(ok) {
			t.Errorf("%s should be part of the CLI channel", ok)
		}
	}
	for _, bad := range []string{
		"/tools/agents/api/cli",              // the root itself is nothing
		"/tools/agents/api/cli/send/extra",   // no sub-paths
		"/tools/agents/api/cli/../tickets",   // no traversal into the other surface
		"/tools/agents/api/tickets",          // the PAT surface
		"/tools/agents/sessions/abc/send",    // the cookie-only surface
		"/tools/agents/api/sessions/abc/send",
		"/api/cli/send", // the public path is rewritten BEFORE this check
	} {
		if isCLIAPIPath(bad) {
			t.Errorf("%s must not be part of the CLI channel", bad)
		}
	}
	// …and a ticket path is not a CLI path, in the other direction.
	if isTicketAPIPath("/tools/agents/api/cli/send") {
		t.Error("the CLI channel leaked into the ticket allowlist")
	}
}

// Every request on this surface has to carry a live token, and the failure
// has to say which of the two things went wrong — an expired token and a
// missing one send a script down different paths.
func TestCLIAuthMiddlewareRejections(t *testing.T) {
	var reached bool
	h := CLIAPIAuthMW(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	call := func(auth string) *httptest.ResponseRecorder {
		reached = false
		r := httptest.NewRequest(http.MethodPost, "/tools/agents/api/cli/send", nil)
		// Local by construction: the locality gate runs first, and these
		// cases are about the TOKEN. httptest's default RemoteAddr is a
		// documentation address, which the gate rightly refuses.
		r.RemoteAddr = "127.0.0.1:51000"
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := call(""); w.Code != http.StatusUnauthorized || reached {
		t.Errorf("no token: code %d reached=%v, want 401 and no handler", w.Code, reached)
	}
	if w := call("Bearer wick_cli_deadbeefdeadbeef"); w.Code != http.StatusUnauthorized || reached {
		t.Errorf("unknown token: code %d reached=%v, want 401", w.Code, reached)
	}
	// A Personal Access Token is a real credential — for the OTHER surface.
	// It must not open this one.
	if w := call("Bearer wick_pat_0123456789abcdef"); w.Code != http.StatusUnauthorized || reached {
		t.Errorf("PAT on the CLI surface: code %d reached=%v, want 401", w.Code, reached)
	}

	// A live token gets through, and the handler sees the grant — not a
	// session id it read off the request.
	g, err := clitoken.Default.Issue("sess-live", "usr-1", "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clitoken.Default.Revoke(g.Token) })

	var seen string
	h2 := CLIAPIAuthMW(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if grant, ok := r.Context().Value(cliSessionKey{}).(clitoken.Grant); ok {
			seen = grant.SessionID
		}
	}))
	r := httptest.NewRequest(http.MethodPost, "/tools/agents/api/cli/send", nil)
	r.RemoteAddr = "127.0.0.1:51000"
	r.Header.Set("Authorization", "Bearer "+g.Token)
	h2.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "sess-live" {
		t.Errorf("handler saw session %q, want the token's own", seen)
	}
}

// The channel is for work running on THIS machine. A request from
// anywhere else is not a use case — it is a leaked token being tried from
// off-box — and it is refused before the token is even read.
func TestCLIChannelIsLocalOnly(t *testing.T) {
	reached := false
	h := CLIAPIAuthMW(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	call := func(remote string, headers map[string]string) *httptest.ResponseRecorder {
		reached = false
		r := httptest.NewRequest(http.MethodPost, "/tools/agents/api/cli/send", nil)
		r.RemoteAddr = remote
		r.Header.Set("Authorization", "Bearer wick_cli_whatever")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// Off-box caller.
	if w := call("203.0.113.9:51000", nil); w.Code != http.StatusForbidden || reached {
		t.Errorf("remote caller: code %d reached=%v, want 403", w.Code, reached)
	}

	// Proxied: the socket is local, the caller is not. The forwarding
	// headers are the only thing that says so.
	for _, h := range []string{"X-Forwarded-For", "X-Real-Ip", "Forwarded"} {
		if w := call("127.0.0.1:51000", map[string]string{h: "203.0.113.9"}); w.Code != http.StatusForbidden {
			t.Errorf("%s present: code %d, want 403", h, w.Code)
		}
	}

	// Local and direct gets as far as the token check (401, not 403) —
	// which is how we know the locality gate let it past.
	if w := call("127.0.0.1:51000", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("local caller: code %d, want 401 from the token check", w.Code)
	}
	if w := call("[::1]:51000", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("IPv6 loopback: code %d, want 401 from the token check", w.Code)
	}
}

// A path outside the channel passes straight through: the middleware must
// not accidentally guard (or open) the rest of the app.
func TestCLIAuthMiddlewareIgnoresOtherPaths(t *testing.T) {
	reached := false
	h := CLIAPIAuthMW(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	r := httptest.NewRequest(http.MethodGet, "/tools/agents/sessions/abc", nil)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !reached {
		t.Error("a non-CLI path should pass through untouched")
	}
}
