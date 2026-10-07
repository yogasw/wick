package omp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"

	"github.com/yogasw/wick/pkg/safeexec"
)

// hitRecorder is a minimal MCP-over-HTTP server that logs every request.
type hitRecorder struct {
	mu   sync.Mutex
	hits []string
}

func (h *hitRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		ID     any    `json:"id"`
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &req)
	h.mu.Lock()
	h.hits = append(h.hits, fmt.Sprintf("%s %s auth=%q method=%s", r.Method, r.URL.Path, r.Header.Get("Authorization"), req.Method))
	h.mu.Unlock()
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var res any = map[string]any{"tools": []any{}}
	if req.Method == "initialize" {
		res = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "probe", "version": "1"}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": res})
}

func (h *hitRecorder) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.hits...)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestE2EOMPMCPIsolation runs the REAL omp binary with a HOME full of
// foreign MCP configs (~/.claude.json, ~/.claude/mcp.json, ~/.cursor,
// project .mcp.json/.cursor/.vscode/opencode.json). Only wick's
// per-session server (with that session's bearer) and the instance's
// extra server may be contacted; the dummy must see nothing.
//
// Gated: WICK_E2E_MCP_ISOLATION=1 and WICK_E2E_OMP_BIN=<path to omp>.
func TestE2EOMPMCPIsolation(t *testing.T) {
	bin := os.Getenv("WICK_E2E_OMP_BIN")
	if os.Getenv("WICK_E2E_MCP_ISOLATION") != "1" || bin == "" {
		t.Skip("set WICK_E2E_MCP_ISOLATION=1 and WICK_E2E_OMP_BIN")
	}
	wick, extra, dummy := &hitRecorder{}, &hitRecorder{}, &hitRecorder{}
	wickSrv, extraSrv, dummySrv := httptest.NewServer(wick), httptest.NewServer(extra), httptest.NewServer(dummy)
	defer wickSrv.Close()
	defer extraSrv.Close()
	defer dummySrv.Close()
	u, _ := url.Parse(wickSrv.URL)
	t.Setenv("WICK_PORT", u.Port())

	root := t.TempDir()
	home := filepath.Join(root, "home")
	ws := filepath.Join(root, "ws")
	t.Setenv("HOME", home)
	t.Setenv("WICK_DATA_DIR", filepath.Join(root, "wick"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")) // would turn ~/.claude on if it leaked
	d := func(tag string) map[string]any {
		return map[string]any{"mcpServers": map[string]any{"dummy-" + tag: map[string]any{"type": "http", "url": dummySrv.URL + "/" + tag}}}
	}
	writeJSON(t, filepath.Join(home, ".claude.json"), d("claude-json"))
	writeJSON(t, filepath.Join(home, ".claude", "mcp.json"), d("claude-mcp"))
	writeJSON(t, filepath.Join(home, ".cursor", "mcp.json"), d("cursor-home"))
	writeJSON(t, filepath.Join(home, ".gemini", "settings.json"), d("gemini-home"))
	writeJSON(t, filepath.Join(home, ".config", "opencode", "opencode.json"), map[string]any{"mcp": map[string]any{"dummy-oc-global": map[string]any{"type": "remote", "url": dummySrv.URL + "/oc-global"}}})
	writeJSON(t, filepath.Join(ws, ".mcp.json"), d("proj-root"))
	writeJSON(t, filepath.Join(ws, ".cursor", "mcp.json"), d("proj-cursor"))
	writeJSON(t, filepath.Join(ws, ".claude", "mcp.json"), d("proj-claude"))
	writeJSON(t, filepath.Join(ws, ".omp", "mcp.json"), d("proj-omp"))
	writeJSON(t, filepath.Join(ws, "opencode.json"), map[string]any{"mcp": map[string]any{"dummy-oc-proj": map[string]any{"type": "remote", "url": dummySrv.URL + "/oc-proj"}}})

	ins := provider.Instance{Type: provider.TypeOMP, Name: "probe", OMPConfig: &provider.OMPConfig{Profile: "wick-probe"},
		Env:             []string{"EXTRA_TOKEN=extra-secret"},
		ExtraMCPServers: fmt.Sprintf(`{"extra":{"type":"http","url":%q,"headers":{"Authorization":"Bearer ${EXTRA_TOKEN}"}}}`, extraSrv.URL+"/mcp")}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	proc, err := Spawner{Binary: bin, MCPToken: "tok-user-1"}.Spawn(ctx, provider.SpawnOptions{
		Workspace: ws, SessionDir: filepath.Join(root, "session"), Instance: &ins, ExtraEnv: ins.Env,
		InitialMessage: "say hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(proc.Stdout())
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	lines := 0
	for sc.Scan() {
		lines++
	}
	_ = proc.Wait()
	for name, h := range map[string]*hitRecorder{"wick": wick, "extra": extra, "dummy": dummy} {
		for _, l := range h.list() {
			t.Logf("%s: %s", name, l)
		}
	}
	t.Logf("omp stdout lines: %d", lines)
	if n := len(dummy.list()); n != 0 {
		t.Fatalf("host MCP leaked into the spawn: %d dummy hits", n)
	}
	gotWick := false
	for _, l := range wick.list() {
		if strings.Contains(l, `auth="Bearer tok-user-1"`) && strings.Contains(l, "method=initialize") {
			gotWick = true
		}
	}
	if !gotWick {
		t.Fatal("wick MCP server not initialized with the session token")
	}
	gotExtra := false
	for _, l := range extra.list() {
		if strings.Contains(l, `auth="Bearer extra-secret"`) {
			gotExtra = true
		}
	}
	if !gotExtra {
		t.Fatal("extra MCP server not contacted with its env-resolved header")
	}
	// Control: the same HOME/workspace WITHOUT wick's isolation must reach
	// the dummy, or the check above proves nothing.
	cctx, ccancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer ccancel()
	ctl := safeexec.CommandContext(cctx, bin, "--profile", "wick-control", "-p", "--mode", "json", "--no-title", "--cwd", ws, "--", "say hi")
	ctl.Dir = ws
	ctl.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"))
	_, _ = ctl.CombinedOutput()
	ctlHits := dummy.list()
	for _, l := range ctlHits {
		t.Logf("control dummy: %s", l)
	}
	if len(ctlHits) == 0 {
		t.Fatal("control run did not reach the dummy either — the probe setup is not exercising host MCP")
	}

	b, _ := os.ReadFile(filepath.Join(home, ".omp", "profiles", "wick-probe", "agent", "mcp.json"))
	if strings.Contains(string(b), "extra-secret") || strings.Contains(string(b), "tok-user-1") {
		t.Fatal("a secret was written into the profile mcp.json")
	}
}
