package omp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// TestE2ERPCRealBinary drives a real `omp --mode rpc` through Spawner in
// server mode, without a login: the model is a models.yml entry pointing
// at a dead endpoint, so a turn never answers — it exercises boot, the
// session header, steer, abort (process stays up), reuse by the next
// turn, and a process that cannot start (no model) failing the turn.
// Opt-in, one omp process at a time, inside a memory-capped scope:
//
//	systemd-run --user --scope -p MemoryMax=900M env \
//	  WICK_E2E_PROVIDER_BIN=/path/omp WICK_E2E_DIR=/scratch \
//	  go test -run TestE2ERPCRealBinary -v ./internal/agents/provider/omp/
//
// WICK_E2E_DIR/home is used as HOME. omp unpacks ~360 MB of natives into
// $HOME/.omp/natives on its first run, which alone overruns a 900 MB cap:
// seed that dir from an existing install first.
func TestE2ERPCRealBinary(t *testing.T) {
	bin, dir := os.Getenv("WICK_E2E_PROVIDER_BIN"), os.Getenv("WICK_E2E_DIR")
	if bin == "" || dir == "" {
		t.Skip("set WICK_E2E_PROVIDER_BIN and WICK_E2E_DIR")
	}
	home := filepath.Join(dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("DEAD_KEY", "x")
	t.Setenv("WICK_PORT", "")
	prevHome := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = prevHome; ShutdownServers() })
	agentDir := profileAgentDir(home, "", "wick-e2e")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := "providers:\n  dead:\n    baseUrl: http://127.0.0.1:9/v1\n    apiKey: DEAD_KEY\n    api: openai-completions\n" +
		"    models:\n      - id: dead-model\n        name: Dead\n        api: openai-completions\n        reasoning: false\n        input: [text]\n        contextWindow: 8000\n        maxTokens: 256\n"
	if err := os.WriteFile(filepath.Join(agentDir, "models.yml"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(dir, "ws")
	_ = os.MkdirAll(ws, 0o755)
	ins := provider.Instance{Type: provider.TypeOMP, Name: "e2e", OMPConfig: &provider.OMPConfig{Profile: "wick-e2e"}}
	spawn := func(t *testing.T, ins provider.Instance, msg string) provider.Process {
		t.Helper()
		p, err := Spawner{Binary: bin}.Spawn(context.Background(), provider.SpawnOptions{
			SessionID: "e2e-session", Workspace: ws, SessionDir: filepath.Join(dir, "sess"),
			InitialMessage: msg, ModelID: "dead/dead-model", Instance: &ins})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	waitLine := func(t *testing.T, r *bufio.Reader, want string) string {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			line, err := r.ReadString('\n')
			if strings.Contains(line, want) {
				return line
			}
			if err != nil {
				t.Fatalf("stream ended before %q: %v", want, err)
			}
		}
		t.Fatalf("no %q in time", want)
		return ""
	}

	start := time.Now()
	p := spawn(t, ins, "hello")
	if _, ok := p.(*rpcProcess); !ok {
		t.Fatalf("server mode spawned %T, want the RPC turn", p)
	}
	r := bufio.NewReader(p.Stdout())
	hdr := waitLine(t, r, `"type":"session"`)
	t.Logf("boot+header in %s: %s", time.Since(start).Round(time.Millisecond), strings.TrimSpace(hdr))
	waitLine(t, r, `"type":"agent_start"`)
	if err := p.(provider.Injector).Inject("and one more thing"); err != nil {
		t.Fatalf("steer on a running turn: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("killed turn Wait = %v, want a cancel", err)
	}
	if rpcServers.Len() != 1 {
		t.Fatalf("abort took the RPC process down (servers=%d)", rpcServers.Len())
	}
	var pid int
	for _, key := range liveKeys() {
		c, _ := rpcServers.Live(key)
		pid = c.Pid()
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status"); err == nil {
			if _, v, ok := strings.Cut(string(b), "VmRSS:"); ok {
				t.Logf("rpc pid %d VmRSS %s", pid, strings.Fields(v)[0]+" kB")
			}
		}
	}

	// The next turn of the session reuses the same process.
	p2 := spawn(t, ins, "again")
	r2 := bufio.NewReader(p2.Stdout())
	waitLine(t, r2, `"type":"agent_start"`)
	_ = p2.Kill()
	_ = p2.Wait()
	for _, key := range liveKeys() {
		if c, _ := rpcServers.Live(key); c.Pid() != pid {
			t.Fatalf("second turn started another process (%d != %d)", c.Pid(), pid)
		}
	}
	ShutdownServers()
	rpcServers = newRPCManager()

	// No model at all: omp exits before ready; the turn fails with its words.
	bad := provider.Instance{Type: provider.TypeOMP, Name: "e2e-bad", OMPConfig: &provider.OMPConfig{Profile: "wick-e2e-nomodel"}}
	pb, err := Spawner{Binary: bin}.Spawn(context.Background(), provider.SpawnOptions{
		SessionID: "e2e-bad", Workspace: ws, SessionDir: filepath.Join(dir, "sess-bad"), InitialMessage: "x", Instance: &bad})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(pb.Stdout())
	if err := pb.Wait(); err != nil {
		t.Fatalf("start failure surfaced as a process error: %v", err)
	}
	if !strings.Contains(string(out), `"stopReason":"error"`) || !strings.Contains(string(out), "No models available") {
		t.Fatalf("start failure stream = %s", out)
	}
}

// liveKeys lists the keys the manager holds (test helper over Live).
func liveKeys() []string { return rpcServers.Keys() }
