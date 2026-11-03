package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/yogasw/wick/internal/agents/clitoken"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
	mcphandlers "github.com/yogasw/wick/internal/mcp/handlers"
	"github.com/yogasw/wick/pkg/tool"
)

// A script run from the shell runs as the token's own session and the
// person who minted it — never a session or a person named in the body —
// and a token that does not resolve never reaches the runner.
func TestCLICodemodeRun(t *testing.T) {
	clitoken.SetSecret(func() string { return "test-secret-0123456789" })
	t.Cleanup(func() { clitoken.SetSecret(func() string { return "" }) })

	prevUsers, prevRun, prevAlive := ticketAPIUsers, cliRunCode, cliSessionAlive
	t.Cleanup(func() { ticketAPIUsers, cliRunCode, cliSessionAlive = prevUsers, prevRun, prevAlive })
	ticketAPIUsers = stubUsers{u: &entity.User{ID: "usr-1", Approved: true}, tags: []string{"tag-a"}}
	cliSessionAlive = func(id string) bool { return id == "sess-live" }

	type seen struct {
		run       mcphandlers.SessionCodeRun
		sessionID string
		userID    string
		tags      []string
	}
	var got *seen
	var runErr error
	cliRunCode = func(_ context.Context, run mcphandlers.SessionCodeRun, sid string, u *entity.User, tags []string) (*codemode.Result, error) {
		got = &seen{run, sid, u.ID, tags}
		if runErr != nil {
			return nil, runErr
		}
		return &codemode.Result{OK: true, Result: json.RawMessage(`{"n":1}`), Calls: []codemode.Call{}}, nil
	}

	h := CLIAPIAuthMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCLICodemodeRun(tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil))
	}))
	call := func(token, body string) *httptest.ResponseRecorder {
		got = nil
		r := httptest.NewRequest(http.MethodPost, "/tools/agents/api/cli/codemode/run", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:51000"
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	live, err := clitoken.Issue("sess-live", "usr-1", "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := call(live.Token, `{"script_path":"scripts/a.ts","session_id":"sess-other","input":{"x":1},"timeout_ms":5000,"lang":"ts"}`)
	if w.Code != http.StatusOK || got == nil {
		t.Fatalf("live token: code %d body %s", w.Code, w.Body)
	}
	if got.sessionID != "sess-live" || got.userID != "usr-1" || len(got.tags) != 1 || got.tags[0] != "tag-a" {
		t.Errorf("ran as %+v, want the token's session, minter and tags", got)
	}
	if got.run.ScriptPath != "scripts/a.ts" || string(got.run.Input) != `{"x":1}` || got.run.Timeout != 5*time.Second || got.run.Lang != "ts" {
		t.Errorf("run = %+v", got.run)
	}
	var res codemode.Result
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !res.OK || string(res.Result) != `{"n":1}` {
		t.Errorf("body %s: %v", w.Body, err)
	}

	// Refused before the runner: no token, a forged one, an expired one.
	expired := signCLIToken(t, "test-secret-0123456789", time.Now().Add(-time.Minute))
	otherKey := signCLIToken(t, "another-secret-9876543210", time.Now().Add(time.Hour))
	for name, tok := range map[string]string{"none": "", "garbage": "wick_cli_deadbeefdeadbeef", "expired": expired, "wrong key": otherKey} {
		if w := call(tok, `{"code":"return 1"}`); w.Code != http.StatusUnauthorized || got != nil {
			t.Errorf("%s token: code %d ran=%v, want 401 and no run", name, w.Code, got != nil)
		}
	}

	// A session that is gone is not run into.
	gone, err := clitoken.Issue("sess-gone", "usr-1", "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(gone.Token, `{"code":"return 1"}`); w.Code != http.StatusNotFound || got != nil {
		t.Errorf("gone session: code %d ran=%v, want 404", w.Code, got != nil)
	}

	// A bad source is the caller's mistake (400); a runner that cannot
	// start is not (503).
	runErr = errors.Join(mcphandlers.ErrCodeSource, errors.New("pass exactly one"))
	if w := call(live.Token, `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad source: code %d, want 400", w.Code)
	}
	runErr = errors.New("no runner")
	if w := call(live.Token, `{"code":"return 1"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("runner down: code %d, want 503", w.Code)
	}
}

// signCLIToken signs a token the way clitoken.Issue does, with any expiry,
// for the cases Issue refuses to mint.
func signCLIToken(t *testing.T, key string, exp time.Time) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sid": "sess-live", "uid": "usr-1", "sub": "sess-live", "exp": exp.Unix(),
	}).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return clitoken.Prefix + signed
}

func TestCLICodemodeRunIsACLIPath(t *testing.T) {
	if !isCLIAPIPath("/tools/agents/api/cli/codemode/run") {
		t.Error("codemode/run should be part of the CLI channel")
	}
	for _, bad := range []string{"/tools/agents/api/cli/codemode", "/tools/agents/api/cli/codemode/run/x"} {
		if isCLIAPIPath(bad) {
			t.Errorf("%s must not be part of the CLI channel", bad)
		}
	}
}
