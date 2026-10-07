package opencode

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
	"sync/atomic"
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

// TestE2EOpencodeMCPIsolation runs the REAL opencode binary with a HOME
// holding a global ~/.config/opencode and ~/.opencode config plus a project
// opencode.json, each declaring a dummy MCP server. Only wick's
// per-session server (with that session's bearer) and the instance's
// extra server may be contacted; the dummy must see nothing.
//
// Gated: WICK_E2E_MCP_ISOLATION=1 and WICK_E2E_OPENCODE_BIN=<path>.
func TestE2EOpencodeMCPIsolation(t *testing.T) {
	bin := os.Getenv("WICK_E2E_OPENCODE_BIN")
	if os.Getenv("WICK_E2E_MCP_ISOLATION") != "1" || bin == "" {
		t.Skip("set WICK_E2E_MCP_ISOLATION=1 and WICK_E2E_OPENCODE_BIN")
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
	oc := func(tag string) map[string]any {
		return map[string]any{"mcp": map[string]any{"dummy-" + tag: map[string]any{"type": "remote", "url": dummySrv.URL + "/" + tag, "enabled": true}}}
	}
	globalCfg := oc("oc-global")
	writeJSON(t, filepath.Join(home, ".opencode", "opencode.json"), oc("oc-home-dot"))
	writeJSON(t, filepath.Join(ws, "opencode.json"), oc("oc-proj"))
	// A git repo, so opencode treats ws as a project and loads its config.
	for _, a := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=p@x", "-c", "user.name=p", "commit", "-qm", "init"}} {
		cmd := safeexec.Command("git", a...)
		cmd.Dir = ws
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	writeJSON(t, filepath.Join(ws, ".opencode", "opencode.json"), oc("oc-proj-dot"))
	writeJSON(t, filepath.Join(home, ".claude.json"), map[string]any{"mcpServers": map[string]any{"dummy-claude": map[string]any{"type": "http", "url": dummySrv.URL + "/claude"}}})

	// The model: the bundled openai provider pointed at a local fake
	// endpoint through the instance's OWN global config dir (per-instance
	// XDG_CONFIG_HOME), so no prompt ever leaves the host.
	var llmCalls atomic.Int32
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llmCalls.Add(1)
		// A hard 401 ends the run at once instead of opencode retrying.
		http.Error(w, `{"error":{"message":"fake","type":"invalid_request_error","code":"invalid_api_key"}}`, http.StatusUnauthorized)
	}))
	defer llmSrv.Close()
	fakeProvider := map[string]any{"openai": map[string]any{"options": map[string]any{"baseURL": llmSrv.URL + "/v1", "apiKey": "fake"}}}
	writeJSON(t, filepath.Join(root, "ocdata", "config", "opencode", "opencode.json"), map[string]any{
		"provider": fakeProvider,
	})
	// The host user's global config: a dummy MCP server, plus the same fake
	// model so the unisolated control run gets far enough to connect MCP.
	globalCfg["provider"] = fakeProvider
	writeJSON(t, filepath.Join(home, ".config", "opencode", "opencode.json"), globalCfg)
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "probe",
		// No login and a non-hosted model: opencode must still connect MCP
		// at startup; the model call itself then fails without credentials.
		OpencodeConfig:  &provider.OpencodeConfig{DataDir: filepath.Join(root, "ocdata"), Model: "openai/gpt-5.5"},
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
		if lines <= 5 {
			t.Logf("stdout: %.300s", sc.Text())
		}
	}
	_ = proc.Wait()
	t.Logf("fake LLM requests: %d", llmCalls.Load())
	for name, h := range map[string]*hitRecorder{"wick": wick, "extra": extra, "dummy": dummy} {
		for _, l := range h.list() {
			t.Logf("%s: %s", name, l)
		}
	}
	t.Logf("opencode stdout lines: %d", lines)
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
	// Control: plain opencode in the same HOME/workspace must reach the
	// dummy, or the check above proves nothing.
	cctx, ccancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer ccancel()
	ctl := safeexec.CommandContext(cctx, bin, "run", "--format", "json", "--model", "openai/gpt-5.5", "say hi")
	ctl.Dir = ws
	ctl.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Join(root, "ctl-data"), "OPENCODE_DISABLE_AUTOUPDATE=true")
	_, _ = ctl.CombinedOutput()
	ctlHits := dummy.list()
	for _, l := range ctlHits {
		t.Logf("control dummy: %s", l)
	}
	if len(ctlHits) == 0 {
		t.Fatal("control run did not reach the dummy either — the probe setup is not exercising host MCP")
	}

	// Deterministic layer check: `opencode debug config` with the exact env
	// a wick spawn gets must resolve every dummy to disabled and keep wick +
	// extra — including the project layer, which the control run above may
	// not reach before its model call fails.
	home2, _ := os.UserHomeDir()
	env, err := spawnEnv(ins, "", "http://127.0.0.1:"+u.Port()+"/mcp", "tok-user-1", foreignMCPNames(ws, home2))
	if err != nil {
		t.Fatal(err)
	}
	dbg := safeexec.CommandContext(cctx, bin, "debug", "config")
	dbg.Dir = ws
	dbg.Env = append(append(os.Environ(), ins.Env...), env...)
	out, err := dbg.Output()
	if err != nil {
		t.Fatalf("debug config: %v", err)
	}
	var resolved struct {
		MCP map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(out[strings.IndexByte(string(out), '{'):], &resolved); err != nil {
		t.Fatalf("debug config output: %v", err)
	}
	for name, v := range resolved.MCP {
		on := v.Enabled == nil || *v.Enabled
		t.Logf("resolved mcp %s enabled=%v", name, on)
		if strings.HasPrefix(name, "dummy-") && on {
			t.Errorf("%s still enabled in a wick spawn", name)
		}
	}
	for _, want := range []string{"dummy-oc-proj", "dummy-oc-proj-dot", "dummy-oc-home-dot"} {
		if _, ok := resolved.MCP[want]; !ok {
			t.Logf("note: %s not present in resolved config (layer isolated or not loaded)", want)
		}
	}
	if w, ok := resolved.MCP["wick"]; !ok || (w.Enabled != nil && !*w.Enabled) {
		t.Error("wick not enabled in resolved config")
	}
	if e, ok := resolved.MCP["extra"]; !ok || (e.Enabled != nil && !*e.Enabled) {
		t.Error("extra not enabled in resolved config")
	}
}
