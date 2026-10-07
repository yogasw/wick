//go:build linux

package terminal

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/managedbin"
)

// TestE2ETerminalOpencodeAuthList installs the REAL gotty through the
// managed installer into a scratch root, starts `opencode auth list` for
// a scratch opencode instance (own data/config/cache/state/HOME), drives
// the gotty websocket through wick's proxy, closes it, and checks no
// process is left.
//
// Gated: WICK_E2E_TERMINAL=1, WICK_E2E_TERMINAL_DIR (scratch root) and
// WICK_E2E_OPENCODE_BIN (an already-installed opencode — this test never
// downloads opencode).
func TestE2ETerminalOpencodeAuthList(t *testing.T) {
	if os.Getenv("WICK_E2E_TERMINAL") != "1" {
		t.Skip("set WICK_E2E_TERMINAL=1 to run a real gotty + opencode")
	}
	root, ocBin := os.Getenv("WICK_E2E_TERMINAL_DIR"), os.Getenv("WICK_E2E_OPENCODE_BIN")
	if root == "" || ocBin == "" {
		t.Fatal("WICK_E2E_TERMINAL_DIR and WICK_E2E_OPENCODE_BIN are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	mb := managedbin.New()
	mb.Root = func() string { return filepath.Join(root, "providers", "bin") }
	j, err := mb.Install(ctx, GottyType, "")
	if err != nil {
		t.Fatalf("install gotty: %v (job %+v)", err, j)
	}
	gotty, ver, ok := mb.CurrentPath(GottyType)
	if !ok {
		t.Fatal("gotty not current after install")
	}
	st, _ := mb.Status(GottyType)
	t.Logf("gotty %s at %s (asset %s)", ver, gotty, j.Tag)
	for _, iv := range st.Installed {
		t.Logf("installed %s sha256=%s version_output=%q", iv.Version, iv.SHA256, iv.VersionOutput)
	}

	home := filepath.Join(root, "home")
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "e2e-scratch", OpencodeConfig: &provider.OpencodeConfig{DataDir: filepath.Join(root, "oc-data")}}
	_ = os.MkdirAll(ins.OpencodeConfig.DataDir, 0o700)
	_ = os.MkdirAll(home, 0o700)
	env := append(Env(ins), "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "XDG_STATE_HOME="+filepath.Join(home, ".state"))
	cmd, _ := LookupCommand(ins, "auth-list")

	m := NewManager()
	s, err := m.Start(StartRequest{Instance: ins, Command: cmd, GottyBin: gotty, CmdBin: ocBin,
		BasePath: "/tools/agents/api/providers/opencode/e2e-scratch/terminal/", User: "e2e", Env: env, Dir: home})
	if err != nil {
		t.Fatal(err)
	}
	pid := s.Pid()
	wick := httptest.NewServer(s)
	defer wick.Close()

	resp, err := http.Get(wick.URL + s.BasePath)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("index through proxy: %v %v", resp, err)
	}
	resp.Body.Close()

	d := websocket.Dialer{Subprotocols: []string{"webtty"}}
	c, _, err := d.Dial("ws"+strings.TrimPrefix(wick.URL, "http")+s.BasePath+"ws", http.Header{"Origin": {wick.URL}})
	if err != nil {
		t.Fatalf("ws through proxy: %v", err)
	}
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"Arguments":"","AuthToken":""}`))
	_ = c.WriteMessage(websocket.TextMessage, []byte(`3{"columns":100,"rows":30}`))
	var out strings.Builder
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "credentials") {
		_ = c.SetReadDeadline(deadline) // one deadline: a timed-out gorilla conn cannot be read again
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Logf("ws read ended: %v", err)
			break
		}
		if len(msg) > 1 && msg[0] == '1' {
			b, _ := base64.StdEncoding.DecodeString(string(msg[1:]))
			out.Write(b)
		}
	}
	remember := snapshot(pid)
	t.Logf("terminal output: %q", out.String())
	if !strings.Contains(strings.ToLower(out.String()), "credentials") {
		t.Errorf("no `opencode auth list` output came through the websocket")
	}
	c.Close()
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("closing the websocket did not end the session")
	}
	time.Sleep(500 * time.Millisecond)
	if alive(pid) {
		t.Errorf("gotty %d still alive", pid)
	}
	for _, r := range remember {
		if alive(r.pid) {
			t.Errorf("child %d still alive", r.pid)
		}
	}
	t.Logf("gotty pid %d and %d children gone", pid, len(remember))
}
