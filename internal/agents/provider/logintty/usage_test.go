package logintty

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestReadUsageClaude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok-123"}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/oauth/usage" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			t.Errorf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 42.5, "resets_at": "2026-09-07T12:00:00Z"},
			"seven_day": {"utilization": 80, "resets_at": "2026-09-10T00:00:00Z"},
			"extra": "ignored"
		}`))
	}))
	defer srv.Close()

	old := anthropicAPIBase
	anthropicAPIBase = srv.URL
	defer func() { anthropicAPIBase = old }()

	windows, err := ReadUsage(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v", windows)
	}
	if windows[0].Key != "five_hour" || windows[0].Utilization != 42.5 {
		t.Fatalf("first window = %+v", windows[0])
	}
	if want := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC); !windows[0].ResetsAt.Equal(want) {
		t.Fatalf("ResetsAt = %v", windows[0].ResetsAt)
	}
	if windows[1].Key != "seven_day" || windows[1].Utilization != 80 {
		t.Fatalf("second window = %+v", windows[1])
	}
}

func TestReadUsageClaudeNoCredentials(t *testing.T) {
	if _, err := ReadUsage(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + t.TempDir()}); err == nil {
		t.Fatal("must error without credentials")
	}
}

func TestReadUsageUnsupportedTypes(t *testing.T) {
	if _, err := ReadUsage(provider.TypeCodex, nil); err != ErrUsageUnsupported {
		t.Fatalf("codex err = %v, want ErrUsageUnsupported", err)
	}
	if _, err := ReadUsage(provider.TypeWick, nil); err != ErrUsageUnsupported {
		t.Fatalf("wick err = %v, want ErrUsageUnsupported", err)
	}
}

// A 429 must be recognisable as a rate limit, not just an opaque status
// string: callers back off on it instead of retrying like a timeout.
func TestReadUsageClaudeRateLimited(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok-123"}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	old := anthropicAPIBase
	anthropicAPIBase = srv.URL
	defer func() { anthropicAPIBase = old }()

	_, err := ReadUsage(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + dir})
	if err == nil {
		t.Fatal("want an error for 429")
	}
	if !IsRateLimited(err) {
		t.Errorf("IsRateLimited = false for %v", err)
	}
	if got := RetryAfterOf(err); got != 2*time.Minute {
		t.Errorf("RetryAfter = %v, want the header's 120s", got)
	}
	// The message stays the same shape as any other endpoint failure —
	// the UI prints it verbatim.
	if err.Error() != "usage endpoint: 429 Too Many Requests" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("90"); got != 90*time.Second {
		t.Errorf("delay-seconds: %v", got)
	}
	// HTTP-date form.
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= time.Minute || got > 2*time.Minute {
		t.Errorf("http-date: %v, want ~2m", got)
	}
	// Junk, absent, and already-past values mean "no server guidance".
	for _, in := range []string{"", "soon", "0", "-5", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)} {
		if got := parseRetryAfter(in); got != 0 {
			t.Errorf("parseRetryAfter(%q) = %v, want 0", in, got)
		}
	}
}

func TestSupportsUsage(t *testing.T) {
	if !SupportsUsage(provider.TypeClaude) {
		t.Error("claude has a usage API")
	}
	for _, ty := range []provider.Type{provider.TypeCodex, provider.TypeGemini, provider.TypeWick} {
		if SupportsUsage(ty) {
			t.Errorf("%s reported as having a usage API", ty)
		}
	}
}

// The identity is what makes "same account, two folders" cost ONE
// request. It must merge on a provable match and never merge two
// different logins.
func TestUsageIdentityMergesSameAccount(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		writeFile(t, filepath.Join(dir, ".credentials.json"),
			`{"claudeAiOauth":{"accessToken":"tok-`+filepath.Base(dir)+`"}}`)
		writeFile(t, filepath.Join(dir, ".claude.json"),
			`{"oauthAccount":{"emailAddress":"Dev@Abc.com"}}`)
	}

	ida := UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + a})
	idb := UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + b})
	if ida != idb {
		t.Errorf("identities differ for one account: %q vs %q", ida, idb)
	}
	// Case-folded, so a differently-cased email is still one account.
	if ida != "claude:email:dev@abc.com" {
		t.Errorf("identity = %q", ida)
	}
}

func TestUsageIdentitySplitsDifferentAccounts(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(a, ".claude.json"), `{"oauthAccount":{"emailAddress":"one@abc.com"}}`)
	writeFile(t, filepath.Join(b, ".claude.json"), `{"oauthAccount":{"emailAddress":"two@abc.com"}}`)

	if UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + a}) ==
		UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + b}) {
		t.Error("two logins share one identity — one account's usage would be shown for both")
	}
}

// No email on disk: the token is the fallback, because an identical
// token IS the same upstream subject (a copied credential dir).
func TestUsageIdentityFallsBackToTokenThenDir(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, dir := range []string{a, b} {
		writeFile(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"same-token"}}`)
	}
	ida := UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + a})
	if ida != UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + b}) {
		t.Error("same token must be one identity")
	}
	if ida == "" || ida == "claude:dir:"+a {
		t.Errorf("identity = %q, want the token hash", ida)
	}
	// Nothing readable at all: the dir, which can split but never merge.
	empty := t.TempDir()
	if got := UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + empty}); got != "claude:dir:"+empty {
		t.Errorf("identity = %q, want the dir fallback", got)
	}
}

// The token must never leak into the key — it is a credential, and the
// key ends up in logs and metrics.
func TestUsageIdentityNeverContainsTheToken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".credentials.json"), `{"claudeAiOauth":{"accessToken":"sk-ant-secret-value"}}`)
	if got := UsageIdentity(provider.TypeClaude, []string{"CLAUDE_CONFIG_DIR=" + dir}); strings.Contains(got, "secret") {
		t.Errorf("identity leaks the token: %q", got)
	}
}
