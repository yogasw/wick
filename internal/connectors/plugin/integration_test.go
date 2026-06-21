package plugin

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// buildEcho compiles cmd/plugins/echo into a temp dir laid out as the loader
// expects: <dir>/connectors/echo/{echo, plugin.json}. Returns <dir>/connectors.
func buildEcho(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	connDir := filepath.Join(root, "connectors", "echo")
	if err := os.MkdirAll(connDir, 0o755); err != nil {
		tb.Fatal(err)
	}
	bin := filepath.Join(connDir, "echo")
	build := exec.Command("go", "build", "-o", bin, "github.com/yogasw/wick/cmd/plugins/echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		tb.Fatalf("build echo: %v", err)
	}
	manifest, err := exec.Command(bin, "--dump-manifest").Output()
	if err != nil {
		tb.Fatalf("dump-manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(connDir, "plugin.json"), manifest, 0o644); err != nil {
		tb.Fatal(err)
	}
	return filepath.Join(root, "connectors")
}

func TestPluginEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + spawns a subprocess")
	}
	dir := buildEcho(t)

	found, err := Scan(dir)
	if err != nil || len(found) != 1 {
		t.Fatalf("scan: %v %d", err, len(found))
	}
	mgr := NewManager(map[string]string{found[0].Key: found[0].BinaryPath}, 5*time.Minute)
	defer mgr.KillAll()

	mod, err := BuildModule(found[0].Manifest, mgr.Client)
	if err != nil {
		t.Fatal(err)
	}
	op := mod.AllOps()[0]

	cctx := connector.NewPluginCtx(context.Background(),
		map[string]string{"token": "s3cr3t"},
		map[string]string{"text": "hello-over-grpc"})
	out, err := op.Execute(cctx)
	if err != nil {
		t.Fatalf("execute over subprocess: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.(json.RawMessage), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["said"] != "hello-over-grpc" || got["token"] != "s3cr3t" {
		t.Fatalf("round trip wrong: %v", got)
	}
}
