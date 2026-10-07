package logintty

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Two omp accounts with usage: every window is tagged with its pool
// account, and Headline keeps the first account's per key.
const ompTwoAccountsFixture = `{
 "reports":[
  {"provider":"openai-codex","limits":[{"id":"p","window":{"id":"5h","durationMs":18000000},"amount":{"usedFraction":0.9}}],"metadata":{"email":"a@example.test"}},
  {"provider":"openai-codex","limits":[{"id":"p","window":{"id":"5h","durationMs":18000000},"amount":{"usedFraction":0.1}},{"id":"s","window":{"id":"7d","durationMs":604800000},"amount":{"usedFraction":0.2}}],"metadata":{"email":"b@example.test"}}
 ],"accountsWithoutUsage":[],"disabledCredentials":[]}`

func TestOMPUsageTaggedPerAccount(t *testing.T) {
	stubOMPUsage(t, ompTwoAccountsFixture)
	ws, err := ReadUsage(provider.TypeOMP, []string{"OMP_PROFILE=tag-test"})
	if err != nil || len(ws) != 3 {
		t.Fatalf("windows %+v %v", ws, err)
	}
	a, _ := AccountWindows(ws, "openai-codex#1")
	b, _ := AccountWindows(ws, "openai-codex#2")
	if len(a) != 1 || a[0].Utilization != 90 || len(b) != 2 || b[0].Utilization != 10 {
		t.Fatalf("per account: a=%+v b=%+v", a, b)
	}
	h := Headline(ws)
	if len(h) != 2 || h[0].Key != "five_hour" || h[0].Utilization != 90 || h[1].Key != "seven_day" || h[0].Account != "" {
		t.Fatalf("headline = %+v", h)
	}
}

func TestHeadlinePassesUntaggedThrough(t *testing.T) {
	in := []UsageWindow{{Key: "five_hour", Utilization: 1}, {Key: "five_hour", Utilization: 2}}
	if got := Headline(in); len(got) != 2 {
		t.Fatalf("untagged reading must be untouched: %+v", got)
	}
	tagged := []UsageWindow{{Account: "x", Error: "expired"}, {Key: "seven_day", Account: "y", Utilization: 5}}
	if got := Headline(tagged); len(got) != 1 || got[0].Key != "seven_day" {
		t.Fatalf("error placeholder must be dropped: %+v", got)
	}
	if _, msg := AccountWindows(tagged, "x"); msg != "expired" {
		t.Fatalf("per-account error = %q", msg)
	}
}

// ompUsage served from cache until agent.db changes: opening a card or the
// popover must not re-exec omp.
func TestOMPListingCachedUntilCredsChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var calls atomic.Int32
	prev := ompRunner
	ompRunner = func(context.Context, []string) ([]byte, error) {
		calls.Add(1)
		return []byte(ompTwoAccountsFixture), nil
	}
	t.Cleanup(func() { ompRunner = prev })
	ompUsageMu.Lock()
	ompUsageCache = map[string]ompUsageEntry{}
	ompUsageMu.Unlock()
	env := []string{"OMP_PROFILE=cache-test"}
	ListAccounts(provider.TypeOMP, env)
	ompUsageMu.Lock()
	key := ompConfigDir(env)
	e := ompUsageCache[key]
	e.at = time.Now().Add(-time.Hour) // older than the failure TTL
	ompUsageCache[key] = e
	ompUsageMu.Unlock()
	ListAccounts(provider.TypeOMP, env)
	if calls.Load() != 1 {
		t.Fatalf("good listing re-fetched without a creds change: %d calls", calls.Load())
	}
	dir := ompConfigDir(env)
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "agent.db"), []byte("x"), 0o600)
	ListAccounts(provider.TypeOMP, env)
	if calls.Load() != 2 {
		t.Fatalf("agent.db changed but listing was not re-read: %d calls", calls.Load())
	}
}

func writeOpencodeAuth(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpencodeUsageAcrossFolders(t *testing.T) {
	data := t.TempDir()
	future := time.Now().Add(time.Hour).UnixMilli()
	past := time.Now().Add(-time.Hour).UnixMilli()
	// Synthetic, non-credential values only.
	writeOpencodeAuth(t, filepath.Join(data, "opencode"),
		`{"openai":{"type":"oauth","access":"tok-main","expires":`+itoa(future)+`,"accountId":"acc-main"},"openrouter":{"type":"api","key":"x"}}`)
	writeOpencodeAuth(t, filepath.Join(data, "accounts", "a2", "opencode"),
		`{"openai":{"type":"oauth","access":"tok-a2","expires":`+itoa(future)+`,"accountId":"acc-a2"}}`)
	writeOpencodeAuth(t, filepath.Join(data, "accounts", "a3", "opencode"),
		`{"openai":{"type":"oauth","access":"tok-a3","expires":`+itoa(past)+`}}`)

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization")+"|"+r.Header.Get("ChatGPT-Account-Id"))
		pct := "12"
		if strings.HasSuffix(r.Header.Get("Authorization"), "tok-a2") {
			pct = "77"
		}
		w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":` + pct + `,"limit_window_seconds":18000,"reset_at":1790000000},"secondary_window":{"used_percent":3,"limit_window_seconds":604800,"reset_after_seconds":60}}}`))
	}))
	t.Cleanup(srv.Close)
	prevURL, prevGap := chatgptUsageURL, opencodeUsageGap
	chatgptUsageURL, opencodeUsageGap = srv.URL, 0
	t.Cleanup(func() { chatgptUsageURL, opencodeUsageGap = prevURL, prevGap })

	env := []string{"XDG_DATA_HOME=" + data}
	ws, err := ReadUsage(provider.TypeOpencode, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[0] != "Bearer tok-main|acc-main" || seen[1] != "Bearer tok-a2|acc-a2" {
		t.Fatalf("requests = %q (expired a3 must not be sent)", seen)
	}
	main, _ := AccountWindows(ws, "main/openai")
	a2, _ := AccountWindows(ws, "a2/openai")
	_, a3err := AccountWindows(ws, "a3/openai")
	if len(main) != 2 || main[0].Key != "five_hour" || main[0].Utilization != 12 || main[0].ResetsAt.Unix() != 1790000000 || main[1].Key != "seven_day" || main[1].ResetsAt.IsZero() {
		t.Fatalf("main = %+v", main)
	}
	if len(a2) != 2 || a2[0].Utilization != 77 {
		t.Fatalf("a2 = %+v", a2)
	}
	if !strings.Contains(a3err, "expired") {
		t.Fatalf("a3 error = %q", a3err)
	}

	accts := ListAccounts(provider.TypeOpencode, env)
	ids := []string{}
	for _, a := range accts {
		ids = append(ids, a.ID)
	}
	if strings.Join(ids, ",") != "main/openai,main/openrouter,a2/openai,a3/openai" {
		t.Fatalf("accounts = %v", ids)
	}
	if accts[0].Plan != "plus" || accts[0].Label != "Account main" || !AccountHasUsage(provider.TypeOpencode, accts[0]) || AccountHasUsage(provider.TypeOpencode, accts[1]) {
		t.Fatalf("row = %+v / %+v", accts[0], accts[1])
	}
}

func TestOpencodeUsageAllFailedIsProbeFailure(t *testing.T) {
	data := t.TempDir()
	writeOpencodeAuth(t, filepath.Join(data, "opencode"), `{"openai":{"type":"oauth","access":"t"}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	prev := chatgptUsageURL
	chatgptUsageURL = srv.URL
	t.Cleanup(func() { chatgptUsageURL = prev })
	_, err := ReadUsage(provider.TypeOpencode, []string{"XDG_DATA_HOME=" + data})
	if !IsRateLimited(err) || RetryAfterOf(err) != 30*time.Second {
		t.Fatalf("err = %v, want the 429 with its Retry-After", err)
	}
	// No ChatGPT login at all: nothing to probe, not an error.
	writeOpencodeAuth(t, filepath.Join(data, "opencode"), `{"openrouter":{"type":"api","key":"x"}}`)
	if ws, err := ReadUsage(provider.TypeOpencode, []string{"XDG_DATA_HOME=" + data}); err != nil || len(ws) != 0 {
		t.Fatalf("no usage source: %v %v", ws, err)
	}
}

func TestChatGPTWindowKey(t *testing.T) {
	for secs, want := range map[float64]string{18000: "five_hour", 604800: "seven_day", 86400: "1d", 7200: "2h", 300: "5m", 0: "window"} {
		if got := chatgptWindowKey(secs); got != want {
			t.Errorf("%v → %q, want %q", secs, got, want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
