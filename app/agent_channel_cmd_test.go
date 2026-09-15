package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A build script branches on the exit code, so each failure has to be a
// DIFFERENT code: "my token expired" and "wick is down" need different
// reactions, and one exit 1 for both makes the script guess.
func TestSendExitCodes(t *testing.T) {
	var status int
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	code := func(err error) int {
		if err == nil {
			return 0
		}
		var coded interface{ ExitCode() int }
		if ok := asCoded(err, &coded); !ok {
			return -1
		}
		return coded.ExitCode()
	}

	status, body = http.StatusOK, `{"status":"queued","session_id":"s1"}`
	if _, err := callCLIAPI(http.MethodPost, srv.URL, "wick_cli_x", "/api/cli/send", map[string]string{"text": "hi"}); err != nil {
		t.Fatalf("a good send should not error: %v", err)
	}

	status, body = http.StatusUnauthorized, `{"error":"token is unknown or expired"}`
	_, err := callCLIAPI(http.MethodPost, srv.URL, "wick_cli_x", "/api/cli/send", map[string]string{"text": "hi"})
	if got := code(err); got != exitAuth {
		t.Errorf("expired token exit = %d, want %d", got, exitAuth)
	}

	status, body = http.StatusUnprocessableEntity, `{"error":"no agent in session"}`
	_, err = callCLIAPI(http.MethodPost, srv.URL, "wick_cli_x", "/api/cli/send", map[string]string{"text": "hi"})
	if got := code(err); got != exitRejected {
		t.Errorf("rejection exit = %d, want %d", got, exitRejected)
	}

	// Nothing listening at all — the case a script hits when the daemon is
	// down, which must not look like an auth problem.
	_, err = callCLIAPI(http.MethodPost, "http://127.0.0.1:1", "wick_cli_x", "/api/cli/send", map[string]string{"text": "hi"})
	if got := code(err); got != exitUnreach {
		t.Errorf("unreachable exit = %d, want %d", got, exitUnreach)
	}

	// No token is a usage error, and it must never reach the network.
	_, err = callCLIAPI(http.MethodPost, srv.URL, "", "/api/cli/send", nil)
	if got := code(err); got != exitUsage {
		t.Errorf("missing token exit = %d, want %d", got, exitUsage)
	}
}

// asCoded is errors.As without importing errors into the test's helper
// signature gymnastics.
func asCoded(err error, target *interface{ ExitCode() int }) bool {
	c, ok := err.(cliExit)
	if !ok {
		return false
	}
	*target = c
	return true
}

func TestMessageBody(t *testing.T) {
	if _, err := messageBody("", ""); err == nil {
		t.Error("an empty message should be refused rather than sent")
	}
	if _, err := messageBody("a", "b"); err == nil {
		t.Error("--text and --file together should be refused")
	}
	// A build log is not a message: it is truncated here, loudly, rather
	// than rejected by the server after the build already finished.
	long := strings.Repeat("x", 9000)
	got, err := messageBody(long, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[truncated") || len([]rune(got)) > 8100 {
		t.Errorf("long message not truncated: %d runes", len([]rune(got)))
	}
}

// whoami is what a script runs first, so its payload has to be readable
// JSON rather than a sentence.
func TestWhoamiDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"session_id":"s1","expires_in":1800}`))
	}))
	defer srv.Close()
	out, err := callCLIAPI(http.MethodGet, srv.URL, "wick_cli_x", "/api/cli/whoami", nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["session_id"] != "s1" {
		t.Fatalf("whoami = %+v", out)
	}
	if _, err := json.Marshal(out); err != nil {
		t.Fatal(err)
	}
}

// A build finishing while wick swaps binaries is not a rare race — deploys
// are exactly when builds run. The report has to survive the boot window.
func TestSendWaitsOutARestart(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"queued","session_id":"s1"}`))
	}))
	// Closed, then reopened by pointing at the live server: the first call
	// has nowhere to land, the retry does.
	down := "http://127.0.0.1:1"

	var out strings.Builder
	if _, err := sendWithRetry(&out, down, "wick_cli_x", "hi", 2*time.Second); err == nil {
		t.Fatal("a dead host should still fail once the budget runs out")
	}
	if !strings.Contains(out.String(), "retrying") {
		t.Errorf("a retry should say so on stderr, got %q", out.String())
	}

	if _, err := sendWithRetry(&out, srv.URL, "wick_cli_x", "hi", 2*time.Second); err != nil {
		t.Fatalf("a live host should deliver: %v", err)
	}
	if hits != 1 {
		t.Errorf("hits = %d, want exactly one (no retry needed)", hits)
	}
	srv.Close()
}

// An expired token does not get better by asking again, and a script
// waiting 90 seconds to learn that is 90 seconds of nobody being told.
func TestSendDoesNotRetryAnAnswer(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"expired"}`))
	}))
	defer srv.Close()

	var out strings.Builder
	start := time.Now()
	_, err := sendWithRetry(&out, srv.URL, "wick_cli_x", "hi", 10*time.Second)
	if err == nil {
		t.Fatal("an expired token should fail")
	}
	if hits != 1 {
		t.Errorf("hits = %d, want 1: an answer is not an outage", hits)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("it waited before reporting an answer it already had")
	}
}
