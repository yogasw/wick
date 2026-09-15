package clitoken

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIssueResolveAndExpiry(t *testing.T) {
	s := New()
	fake := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fake }

	g, err := s.Issue("sess-1", "usr-1", "build 0.1.255", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g.Token, Prefix) {
		t.Fatalf("token %q should carry the CLI prefix", g.Token)
	}
	got, ok := s.Resolve(g.Token)
	if !ok || got.SessionID != "sess-1" || got.UserID != "usr-1" {
		t.Fatalf("resolve = %+v %v, want the grant back", got, ok)
	}

	// A token from another issuer must never resolve here — that is what
	// keeps a PAT or a sub-agent token from reaching this surface.
	if _, ok := s.Resolve("wick_pat_deadbeef"); ok {
		t.Error("a foreign prefix resolved")
	}

	// One second past the expiry it is gone, and gone for good.
	fake = fake.Add(30*time.Minute + time.Second)
	if _, ok := s.Resolve(g.Token); ok {
		t.Error("an expired token still resolved")
	}
	if s.Revoke(g.Token) {
		t.Error("an expired token should already have been dropped")
	}
}

// "Just make it long" is how a build credential becomes a standing one, so
// the ceiling is enforced here rather than trusted to callers.
func TestTTLIsClamped(t *testing.T) {
	s := New()
	for _, tc := range []struct{ ask, want time.Duration }{
		{0, DefaultTTL},
		{-time.Hour, DefaultTTL},
		{time.Second, MinTTL},
		{24 * time.Hour, MaxTTL},
		{45 * time.Minute, 45 * time.Minute},
	} {
		g, err := s.Issue("sess-1", "usr-1", "", tc.ask)
		if err != nil {
			t.Fatal(err)
		}
		if got := time.Until(g.ExpiresAt).Round(time.Minute); got != tc.want.Round(time.Minute) {
			t.Errorf("ttl %v -> %v, want %v", tc.ask, got, tc.want)
		}
	}
}

// A token with no session is meaningless: the session IS the authorisation.
func TestIssueRefusesASessionlessToken(t *testing.T) {
	if _, err := New().Issue("  ", "usr-1", "", time.Minute); err == nil {
		t.Fatal("a token with no session should be refused")
	}
}

// A listing shows what is outstanding without handing back the secret —
// otherwise "list" becomes a way to recover a token somebody lost.
func TestListForMasksTheSecretAndScopesToTheSession(t *testing.T) {
	s := New()
	a, _ := s.Issue("sess-1", "usr-1", "one", time.Hour)
	_, _ = s.Issue("sess-2", "usr-1", "other session", time.Hour)

	list := s.ListFor("sess-1")
	if len(list) != 1 {
		t.Fatalf("list = %d, want only this session's", len(list))
	}
	if strings.Contains(list[0].Token, strings.TrimPrefix(a.Token, Prefix)[:8]) {
		t.Errorf("the listing leaked the token: %q", list[0].Token)
	}

	if n := s.RevokeSession("sess-1"); n != 1 {
		t.Errorf("revoked %d, want 1", n)
	}
	if _, ok := s.Resolve(a.Token); ok {
		t.Error("a revoked session's token still resolves")
	}
}

// The address is half the credential: a token with the wrong base URL is a
// script that cannot report. On a host behind a proxy the loopback port is
// not the door — it answers 403 from the gate, which reads like an auth
// problem and is not one.
func TestBaseURL(t *testing.T) {
	t.Cleanup(func() { SetBaseURL(func() string { return "" }) })

	SetBaseURL(func() string { return "" })
	if got := BaseURL(); got != "http://127.0.0.1:9425" {
		t.Errorf("unconfigured = %q, want the loopback fallback", got)
	}

	SetBaseURL(func() string { return "https://wick.example.com/" })
	if got := BaseURL(); got != "https://wick.example.com" {
		t.Errorf("configured = %q, want it without the trailing slash", got)
	}

	// A nil resolver must not replace a working one.
	SetBaseURL(nil)
	if got := BaseURL(); got != "https://wick.example.com" {
		t.Errorf("after nil = %q, want the previous resolver", got)
	}
}

// Handing out an address without checking it is how this failed its first
// live test: the obvious loopback port answered 403 from the host gate,
// which reads like an auth problem and is not.
func TestPickReachableTriesUntilSomethingAnswers(t *testing.T) {
	var gotAuth string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/cli/whoami" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"s1"}`))
	}))
	defer ok.Close()
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer gate.Close()

	// The gated host is tried first and rejected; the working one wins.
	got, err := PickReachable(t.Context(), "wick_cli_abc", []string{gate.URL, ok.URL})
	if err != nil {
		t.Fatalf("a reachable candidate should win: %v", err)
	}
	if got != ok.URL {
		t.Errorf("picked %q, want %q", got, ok.URL)
	}
	if gotAuth != "Bearer wick_cli_abc" {
		t.Errorf("probe sent %q — it must carry the very token it is verifying", gotAuth)
	}

	// Nothing answers: the caller is told WHICH address failed and how,
	// because "it did not work" is not something a person can act on.
	_, err = PickReachable(t.Context(), "wick_cli_abc", []string{gate.URL})
	if err == nil {
		t.Fatal("a gated host is not reachable")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), gate.URL) {
		t.Errorf("error = %v, want the address and the status", err)
	}

	if _, err := PickReachable(t.Context(), "t", nil); err == nil {
		t.Error("no candidates should be an error, not a silent empty string")
	}
}

// The order matters: the configured public URL is the name the host
// allowlist is written in, so it is tried before the machine's own port.
func TestCandidatesPreferThisMachine(t *testing.T) {
	SetBaseURL(func() string { return "https://wick.example.com/" })
	t.Cleanup(func() { SetBaseURL(func() string { return "" }) })

	c := Candidates()
	// This machine first: a build script runs HERE, and a request that
	// leaves for the public name to be routed back by a proxy is a longer
	// path with more ways to fail.
	if len(c) < 3 || c[0] != LoopbackURL() {
		t.Fatalf("candidates = %v, want this machine's own port first", c)
	}
	// Both loopback spellings, because an allowlist may name one and not
	// the other — which is precisely what happened on this host.
	if c[1] != "http://localhost:9425" {
		t.Errorf("candidates = %v, want the localhost spelling too", c)
	}
	if c[len(c)-1] != "https://wick.example.com" {
		t.Errorf("candidates = %v, want the configured URL last (trimmed)", c)
	}
}
