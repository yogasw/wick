package plugin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// echoBinName is the plugin binary filename, with the ".exe" suffix Windows
// requires to exec it — mirrors what cmd/cli plugin build writes per-GOOS, so
// the test binary is named (and recorded in the manifest) exactly as in prod.
func echoBinName() string {
	if runtime.GOOS == "windows" {
		return "echo.exe"
	}
	return "echo"
}

// buildEcho compiles cmd/plugins/echo into a temp dir laid out as the loader
// expects: <dir>/connectors/echo/{echo, plugin.json}. Returns <dir>/connectors.
func buildEcho(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	connDir := filepath.Join(root, "connectors", "echo")
	if err := os.MkdirAll(connDir, 0o755); err != nil {
		tb.Fatal(err)
	}
	bin := filepath.Join(connDir, echoBinName())
	build := safeexec.Command("go", "build", "-o", bin, "github.com/yogasw/wick/cmd/plugins/echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		tb.Fatalf("build echo: %v", err)
	}
	manifest, err := safeexec.Command(bin, "--dump-manifest").Output()
	if err != nil {
		tb.Fatalf("dump-manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(connDir, "plugin.json"), manifest, 0o644); err != nil {
		tb.Fatal(err)
	}
	return filepath.Join(root, "connectors")
}

func buildEchoSigned(t *testing.T, keyPath string) string {
	t.Helper()
	root := t.TempDir()
	connDir := filepath.Join(root, "connectors", "echo")
	if err := os.MkdirAll(connDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(connDir, echoBinName())
	build := safeexec.Command("go", "build", "-o", bin, "github.com/yogasw/wick/cmd/plugins/echo")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build echo: %v", err)
	}
	manifest, err := safeexec.Command(bin, "--dump-manifest", "--sign-key", keyPath).Output()
	if err != nil {
		t.Fatalf("dump-manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(connDir, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
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
	if err := wickplugin.VerifyManifest(found[0].Manifest, found[0].BinaryPath); err != nil {
		t.Fatalf("verify: %v", err)
	}
	mgr := NewManager(map[string]string{found[0].Key: found[0].BinaryPath}, 5*time.Minute)
	defer mgr.KillAll()

	mod := BuildModule(found[0].Manifest.Module, mgr.Client)
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

func TestPluginVerifyRejectsTamperedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a subprocess binary")
	}
	dir := buildEcho(t)
	found, err := Scan(dir)
	if err != nil || len(found) != 1 {
		t.Fatalf("scan: %v %d", err, len(found))
	}
	f, err := os.OpenFile(found[0].BinaryPath, os.O_APPEND|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{0x00})
	f.Close()
	if err := wickplugin.VerifyManifest(found[0].Manifest, found[0].BinaryPath); err == nil {
		t.Fatal("tampered binary must fail verification")
	}
}

func TestPluginSignedEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + spawns a subprocess")
	}
	priv, pub := wickplugin.GenerateKeypair()
	keyPath := filepath.Join(t.TempDir(), "k.key")
	if err := os.WriteFile(keyPath, []byte(priv), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := buildEchoSigned(t, keyPath)
	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "1")
	t.Setenv("WICK_PLUGIN_PUBKEY", pub)

	found, _ := Scan(dir)
	if len(found) != 1 {
		t.Fatalf("scan: %d", len(found))
	}
	if err := wickplugin.VerifyManifest(found[0].Manifest, found[0].BinaryPath); err != nil {
		t.Fatalf("signed plugin should verify under require-mode: %v", err)
	}
	mgr := NewManager(map[string]string{found[0].Key: found[0].BinaryPath}, 5*time.Minute)
	defer mgr.KillAll()
	mod := BuildModule(found[0].Manifest.Module, mgr.Client)
	op := mod.AllOps()[0]
	cctx := connector.NewPluginCtx(context.Background(), map[string]string{"token": "s"}, map[string]string{"text": "hi"})
	out, err := op.Execute(cctx)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got map[string]string
	json.Unmarshal(out.(json.RawMessage), &got)
	if got["said"] != "hi" {
		t.Fatalf("round trip: %v", got)
	}
}

func TestPluginStreamLargeResult(t *testing.T) {
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
	lease, err := mgr.Client(found[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	// A >2 MiB text forces multiple 1 MiB chunks through ExecuteStream.
	big := strings.Repeat("Z", (1<<20)*2)
	out, err := lease.Conn.ExecuteStream(context.Background(), wickplugin.ExecCall{
		Operation: found[0].Manifest.Module.AllOps()[0].Key,
		Input:     map[string]string{"text": big},
		Creds:     map[string]string{"token": "s"},
	})
	if err != nil {
		t.Fatalf("ExecuteStream over subprocess: %v", err)
	}
	if !strings.Contains(string(out), big) {
		t.Fatal("streamed result missing the large payload")
	}
}
