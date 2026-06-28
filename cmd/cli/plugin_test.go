package cli

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/safeexec"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// samplePluginMain is a minimal connector plugin source the test compiles. It
// imports the real pkg/plugin + pkg/connector so the built binary's
// --dump-manifest produces a genuine envelope — exactly what a plugins repo
// connector/<name>/main.go looks like.
const samplePluginMain = `package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/wickdocs"
)

func main() {
	say := func(c *connector.Ctx) (any, error) { return c.Input("text"), nil }
	mod := connector.Module{
		Meta:    connector.Meta{Key: "sample", Name: "Sample"},
		Configs: entity.StructToConfigs(struct{ Token string ` + "`wick:\"token,secret\"`" + ` }{}),
		Operations: []connector.Category{
			connector.Cat("Main", "", connector.Op("say", "Say", "echo",
				struct{ Text string ` + "`wick:\"text\"`" + ` }{}, say, wickdocs.Docs{})),
		},
	}
	wickplugin.Serve(mod)
}
`

// chdir switches into dir and registers a cleanup that restores the previous
// working directory. Tests using it must NOT be t.Parallel().
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// findModuleRoot walks up from the test's CWD to the wick module root (the dir
// holding go.mod with module github.com/yogasw/wick) so the temp plugin can
// `replace` it and compile against local source.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			first := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
			if first == "module github.com/yogasw/wick" {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("wick module root not found above test CWD")
		}
		dir = parent
	}
}

// TestBuildOnePlugin_HostTarget builds a sample connector plugin for the host
// os/arch and asserts the resulting zip contains a binary + plugin.json that
// round-trip through VerifyManifest — the full production→consumption contract.
func TestBuildOnePlugin_HostTarget(t *testing.T) {
	moduleRoot := findModuleRoot(t)

	// Lay out a plugins-style repo: go.mod (replace -> local wick) +
	// connector/sample/{main.go,VERSION}.
	repo := t.TempDir()
	connSrc := filepath.Join(repo, "connector", "sample")
	if err := os.MkdirAll(connSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(connSrc, "main.go"), []byte(samplePluginMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(connSrc, "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module wickpluginstest\n\ngo 1.25.0\n\nrequire github.com/yogasw/wick v0.0.0\n\nreplace github.com/yogasw/wick => " + filepath.ToSlash(moduleRoot) + "\n"
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	chdir(t, repo)

	if out, err := runGo(t, "mod", "tidy"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}

	outDir := filepath.Join(repo, "bin")
	zipPath, err := buildOnePlugin("connector", "sample", runtime.GOOS, runtime.GOARCH, outDir, "", "")
	if err != nil {
		t.Fatalf("buildOnePlugin: %v", err)
	}

	wantName := "sample-1.2.3-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip"
	if filepath.Base(zipPath) != wantName {
		t.Errorf("zip name = %s, want %s", filepath.Base(zipPath), wantName)
	}

	extract := t.TempDir()
	bin, manifestPath := unzipPlugin(t, zipPath, extract)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var m wickplugin.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if m.Module.Meta.Key != "sample" {
		t.Errorf("manifest key = %q, want sample", m.Module.Meta.Key)
	}
	if m.Version != "1.2.3" {
		t.Errorf("manifest version = %q, want 1.2.3", m.Version)
	}
	if m.Kind != wickplugin.KindConnector {
		t.Errorf("manifest kind = %q, want %q", m.Kind, wickplugin.KindConnector)
	}
	wantArch := runtime.GOOS + "/" + runtime.GOARCH
	if len(m.OSArch) != 1 || m.OSArch[0] != wantArch {
		t.Errorf("manifest os_arch = %v, want [%s]", m.OSArch, wantArch)
	}
	if err := wickplugin.VerifyManifest(m, bin); err != nil {
		t.Errorf("VerifyManifest failed on built artifact: %v", err)
	}
}

// TestBuildOnePlugin_KeyFolderMismatch asserts the build fails when Meta.Key
// (here "sample") doesn't equal the folder name ("wrongname") — the one-identity
// rule that keeps zip / install dir / registry key from drifting.
func TestBuildOnePlugin_KeyFolderMismatch(t *testing.T) {
	moduleRoot := findModuleRoot(t)
	repo := t.TempDir()
	// Folder is "wrongname" but the source sets Meta.Key = "sample".
	connSrc := filepath.Join(repo, "connector", "wrongname")
	if err := os.MkdirAll(connSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(connSrc, "main.go"), []byte(samplePluginMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(connSrc, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module wickpluginstest\n\ngo 1.25.0\n\nrequire github.com/yogasw/wick v0.0.0\n\nreplace github.com/yogasw/wick => " + filepath.ToSlash(moduleRoot) + "\n"
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, repo)
	if out, err := runGo(t, "mod", "tidy"); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}

	_, err := buildOnePlugin("connector", "wrongname", runtime.GOOS, runtime.GOARCH, filepath.Join(repo, "bin"), "", "")
	if err == nil {
		t.Fatal("expected build to fail on Key/folder mismatch")
	}
	if !strings.Contains(err.Error(), "must equal the folder name") {
		t.Errorf("error should explain the Key/folder rule, got: %v", err)
	}
}

func runGo(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := safeexec.Command("go", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// unzipPlugin extracts zipPath into dst and returns the paths of the binary
// (the non-plugin.json entry) and plugin.json.
func unzipPlugin(t *testing.T, zipPath, dst string) (bin, manifest string) {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target := filepath.Join(dst, zf.Name)
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode())
		if err != nil {
			t.Fatal(err)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, rc); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
		w.Close()
		rc.Close()
		if zf.Name == "plugin.json" {
			manifest = target
		} else {
			bin = target
		}
	}
	if bin == "" || manifest == "" {
		t.Fatalf("zip missing binary or plugin.json (bin=%q manifest=%q)", bin, manifest)
	}
	return bin, manifest
}
