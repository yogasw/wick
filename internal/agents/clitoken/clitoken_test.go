package clitoken

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// signing sets the key these tokens are signed with, the way the app does
// at boot. Without one a token cannot be minted at all, which is itself
// the right behaviour: an unsigned bearer would be a password anybody can
// write.
func signing(t *testing.T) {
	t.Helper()
	SetSecret(func() string { return "test-secret-0123456789" })
	t.Cleanup(func() { SetSecret(func() string { return "" }) })
}

// freeze pins the package clock so expiry can be tested without sleeping.
func freeze(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	now = func() time.Time { return cur }
	t.Cleanup(func() { now = time.Now })
	return &cur
}

func TestIssueResolveAndExpiry(t *testing.T) {
	signing(t)
	clock := freeze(t, time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC))

	g, err := Issue("sess-1", "usr-1", "build 0.1.261", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(g.Token, Prefix) {
		t.Fatalf("token %q should carry the CLI prefix", g.Token)
	}
	got, ok := Resolve(g.Token)
	if !ok || got.SessionID != "sess-1" || got.UserID != "usr-1" || got.Note != "build 0.1.261" {
		t.Fatalf("resolve = %+v %v, want the grant back", got, ok)
	}

	// A token from another issuer must never resolve here — that is what
	// keeps a PAT or a sub-agent token from reaching this surface.
	if _, ok := Resolve("wick_pat_0123456789abcdef"); ok {
		t.Error("a foreign prefix resolved")
	}
	// …and neither does a forged payload: the signature is the whole check.
	if _, ok := Resolve(Prefix + "not.a.jwt"); ok {
		t.Error("a malformed token resolved")
	}

	// Past its expiry the token refuses itself: the claim is inside the
	// signed payload, so nothing has to remember it.
	*clock = clock.Add(31 * time.Minute)
	if _, ok := Resolve(g.Token); ok {
		t.Error("an expired token still resolved")
	}
}

// The point of signing: a process that never saw the mint can still verify
// it. That is what makes a report survive the swap it is reporting on —
// the successor was not running when the token was issued.
func TestATokenVerifiesWithoutTheIssuer(t *testing.T) {
	signing(t)
	minted, err := Issue("sess-1", "usr-1", "build", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := Resolve(minted.Token) // no shared state involved
	if !ok || got.ID != minted.ID {
		t.Fatalf("resolve = %+v %v", got, ok)
	}

	// Only under the right key. Rotating the app's session secret is the
	// documented way to void every outstanding token at once.
	SetSecret(func() string { return "a-different-secret" })
	if _, ok := Resolve(minted.Token); ok {
		t.Error("a token verified under the wrong key")
	}
}

// "Just make it long" is how a build credential becomes a standing one, so
// the ceiling is enforced here rather than trusted to callers.
func TestTTLIsClamped(t *testing.T) {
	signing(t)
	for _, tc := range []struct{ ask, want time.Duration }{
		{0, DefaultTTL},
		{-time.Hour, DefaultTTL},
		{time.Second, MinTTL},
		{24 * time.Hour, MaxTTL},
		{45 * time.Minute, 45 * time.Minute},
	} {
		g, err := Issue("sess-1", "usr-1", "", tc.ask)
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
	signing(t)
	if _, err := Issue("  ", "usr-1", "", time.Minute); err == nil {
		t.Fatal("a token with no session should be refused")
	}
}

// No key, no token. An unsigned bearer would be a password anybody could
// write, so minting fails loudly rather than handing one out.
func TestIssueRefusesWithoutASigningKey(t *testing.T) {
	SetSecret(func() string { return "" })
	if _, err := Issue("sess-1", "usr-1", "", time.Minute); err == nil {
		t.Fatal("minting without a signing key should be refused")
	}
}

// Handing out an address without checking it is how this failed its first
// live test: the obvious port answered 403 from the host gate, which reads
// like an auth problem and is not.
func TestPickReachableTriesUntilSomethingAnswers(t *testing.T) {
	signing(t)
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

// This machine only: the channel refuses anything that did not come from
// here, so advertising the public URL would hand out an address that
// cannot work. Both spellings, because an allowlist can name one and not
// the other — which is exactly what this host had.
func TestCandidatesAreLoopbackOnly(t *testing.T) {
	SetBaseURL(func() string { return "https://wick.example.com/" })
	t.Cleanup(func() { SetBaseURL(func() string { return "" }) })

	c := Candidates()
	if len(c) != 2 || c[0] != LoopbackURL() || c[1] != "http://localhost:9425" {
		t.Fatalf("candidates = %v, want both loopback spellings and nothing else", c)
	}
	for _, got := range c {
		if strings.Contains(got, "wick.example.com") {
			t.Errorf("the configured public URL must not be offered: %v", c)
		}
	}
}
