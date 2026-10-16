package agents

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/storage"
)

// fakeWickToken builds a wick_sub_ token shaped like signScopedToken's. The
// signature is junk on purpose: the reader must not care about it.
func fakeWickToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := base64.RawURLEncoding
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	return "wick_sub_" + enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) +
		"." + enc.EncodeToString(payload) + ".c2VjcmV0LXNpZ25hdHVyZS1kby1ub3QtbGVhaw"
}

func claudeArgv(t *testing.T, bearer string) []string {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"wick": map[string]any{
				"type":    "http",
				"url":     "http://127.0.0.1:8080/mcp",
				"headers": map[string]string{"Authorization": "Bearer " + bearer, "X-Wick-Session-Id": "hdr-session"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return []string{"claude", "-p", "--mcp-config", string(cfg), "--allowedTools", "mcp__wick"}
}

// withFakeProc swaps procRead for a map of pid -> file -> content and
// clears the per-pid cache so tests do not see each other's answers.
func withFakeProc(t *testing.T, files map[int]map[string][]string) {
	t.Helper()
	orig := procRead
	procRead = func(pid int, file string) ([]byte, error) {
		f, ok := files[pid][file]
		if !ok {
			return nil, errors.New("permission denied")
		}
		return []byte(strings.Join(f, "\x00") + "\x00"), nil
	}
	wickClaimsMu.Lock()
	wickClaimsCache = map[int]wickClaimsCacheEntry{}
	wickClaimsMu.Unlock()
	t.Cleanup(func() {
		procRead = orig
		wickClaimsMu.Lock()
		wickClaimsCache = map[int]wickClaimsCacheEntry{}
		wickClaimsMu.Unlock()
	})
}

func TestWickClaimsFromCmdline_Claude(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	tok := fakeWickToken(t, map[string]any{"uid": "u1", "sid": "sess-a--sub-x1", "sub": "u1", "exp": exp})
	c, ok := wickClaimsFromCmdline(claudeArgv(t, tok))
	if !ok {
		t.Fatal("claude --mcp-config token not detected")
	}
	if c.UserID != "u1" || c.SessionID != "sess-a--sub-x1" || c.Expires != exp {
		t.Fatalf("claims = %+v", c)
	}
	// The equals form is accepted too.
	argv := claudeArgv(t, tok)
	eq := []string{argv[0], "--mcp-config=" + argv[3]}
	if _, ok := wickClaimsFromCmdline(eq); !ok {
		t.Fatal("--mcp-config=<json> not detected")
	}
}

func TestWickClaimsFromEnviron(t *testing.T) {
	tok := fakeWickToken(t, map[string]any{"sub": "u2", "sid": "sess-b"})
	c, ok := wickClaimsFromEnviron([]string{"HOME=/home/x", "WICK_MCP_URL=http://127.0.0.1/mcp", "WICK_MCP_TOKEN=" + tok})
	if !ok {
		t.Fatal("WICK_MCP_TOKEN not detected")
	}
	// uid missing → sub fills it.
	if c.UserID != "u2" || c.SessionID != "sess-b" {
		t.Fatalf("claims = %+v", c)
	}
}

func TestWickClaims_NotDetected(t *testing.T) {
	good := fakeWickToken(t, map[string]any{"uid": "u1", "sid": "s"})
	parts := strings.SplitN(strings.TrimPrefix(good, "wick_sub_"), ".", 3)
	cases := map[string]string{
		"no prefix":         strings.TrimPrefix(good, "wick_sub_"),
		"pat":               "wick_pat_abc",
		"opaque wick_sub_":  "wick_sub_" + strings.Repeat("ab", 32),
		"two segments":      "wick_sub_" + parts[0] + "." + parts[1],
		"payload not b64":   "wick_sub_" + parts[0] + ".!!!." + parts[2],
		"payload not json":  "wick_sub_" + parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + "." + parts[2],
		"no uid/sub/sid":    fakeWickToken(t, map[string]any{"exp": 1}),
		"empty":             "",
		"empty payload seg": "wick_sub_" + parts[0] + ".." + parts[2],
	}
	for name, tok := range cases {
		if _, ok := wickClaimsFromBearer(tok); ok {
			t.Errorf("%s: detected, want not detected", name)
		}
		if _, ok := wickClaimsFromEnviron([]string{"WICK_MCP_TOKEN=" + tok}); ok {
			t.Errorf("%s (environ): detected, want not detected", name)
		}
		if _, ok := wickClaimsFromCmdline(claudeArgv(t, tok)); ok {
			t.Errorf("%s (cmdline): detected, want not detected", name)
		}
	}
	// A config path instead of inline JSON is never followed.
	if _, ok := wickClaimsFromCmdline([]string{"claude", "--mcp-config", "/tmp/mcp.json"}); ok {
		t.Error("path --mcp-config detected")
	}
	// A token under another server name is not wick's.
	argv := claudeArgv(t, good)
	argv[3] = strings.Replace(argv[3], `"wick"`, `"other"`, 1)
	if _, ok := wickClaimsFromCmdline(argv); ok {
		t.Error("non-wick mcp server detected")
	}
}

func TestRootSessionID(t *testing.T) {
	for in, want := range map[string]string{
		"abc":               "abc",
		"abc--sub-123":      "abc",
		"abc--sub-1--sub-2": "abc",
		"--sub-x":           "--sub-x",
	} {
		if got := rootSessionID(in); got != want {
			t.Errorf("rootSessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

// End to end through a fake /proc and a real layout: a sub-agent sid
// resolves its title and project from the root session, and neither the
// returned row nor anything logged on the way carries the token.
func TestDetectWickSession_ResolvesAndNeverLeaksToken(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	prevLayout := globalLayout
	globalLayout = layout
	t.Cleanup(func() { globalLayout = prevLayout })

	if err := storage.WriteJSON(layout.SessionMeta("root-1"), map[string]any{
		"label": "Debug webhook 401", "project_id": "proj-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(layout, project.CreateOptions{ID: "proj-1", Name: "Support Ops"}); err != nil {
		t.Fatal(err)
	}

	tok := fakeWickToken(t, map[string]any{"uid": "u1", "sid": "root-1--sub-abc"})
	codexTok := fakeWickToken(t, map[string]any{"uid": "u9", "sid": "missing-session"})
	withFakeProc(t, map[int]map[string][]string{
		100: {"cmdline": claudeArgv(t, tok)},
		200: {"cmdline": {"codex", "exec"}, "environ": {"WICK_MCP_TOKEN=" + codexTok}},
		300: {"cmdline": {"claude", "-p"}}, // environ unreadable
	})

	var logBuf bytes.Buffer
	prevLog := log.Writer()
	log.SetOutput(&logBuf)
	prevSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { log.SetOutput(prevLog); slog.SetDefault(prevSlog) })

	row, ok := detectWickSession(100)
	if !ok || !row.Detected {
		t.Fatal("claude row not detected")
	}
	if row.SessionID != "root-1--sub-abc" || row.Title != "Debug webhook 401" ||
		row.ProjectID != "proj-1" || row.Project != "Support Ops" {
		t.Fatalf("row = %+v", row)
	}

	unresolved, ok := detectWickSession(200)
	if !ok || !unresolved.Detected || unresolved.SessionID != "missing-session" {
		t.Fatalf("codex row = %+v ok=%v", unresolved, ok)
	}
	if unresolved.Title != "" || unresolved.Project != "" {
		t.Fatalf("missing session resolved to %+v", unresolved)
	}

	if _, ok := detectWickSession(300); ok {
		t.Fatal("process with no token detected")
	}
	if _, ok := detectWickSession(999); ok {
		t.Fatal("vanished process detected")
	}

	for _, r := range []wickSessionRow{row, unresolved} {
		js, _ := json.Marshal(r)
		dump := string(js) + fmt.Sprintf("%+v %#v", r, r) + logBuf.String()
		for _, secret := range []string{tok, codexTok, "c2VjcmV0LXNpZ25hdHVyZS1kby1ub3QtbGVhaw", "wick_sub_", "Bearer"} {
			if strings.Contains(dump, secret) {
				t.Fatalf("token material %q leaked into output/log: %s", secret, dump)
			}
		}
	}
}

// A crafted sid must never reach the filesystem as a path.
func TestDetectWickSession_TraversalSidIgnored(t *testing.T) {
	prevLayout := globalLayout
	globalLayout = agentconfig.NewLayout(t.TempDir())
	t.Cleanup(func() { globalLayout = prevLayout })

	tok := fakeWickToken(t, map[string]any{"uid": "u1", "sid": "../../etc"})
	withFakeProc(t, map[int]map[string][]string{1: {"environ": {"WICK_MCP_TOKEN=" + tok}}})
	row, ok := detectWickSession(1)
	if !ok || row.Title != "" || row.ProjectID != "" {
		t.Fatalf("row = %+v ok=%v", row, ok)
	}
}
