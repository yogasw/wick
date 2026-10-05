package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// writeService lays out dir/<key>/{bin,plugin.json} with a manifest that
// passes VerifyManifest for the given route prefix.
func writeService(t *testing.T, dir, key, version, prefix string) {
	t.Helper()
	pd := filepath.Join(dir, key)
	if err := os.MkdirAll(pd, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := []byte("fake binary " + version)
	if err := os.WriteFile(filepath.Join(pd, "bin"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bin)
	meta := wickplugin.ToolMeta{Key: key, Name: key}
	m := wickplugin.Manifest{
		SchemaVersion: 1, Kind: wickplugin.KindService, Version: version,
		ProtoVersion: wickplugin.ProtoVersion, Entry: "bin",
		OSArch: []string{runtime.GOOS + "/" + runtime.GOARCH}, SHA256: hex.EncodeToString(sum[:]),
		Module:  connector.Module{Meta: connector.Meta{Key: key, Name: key}},
		Service: &wickplugin.ServiceModule{Meta: meta, Routes: []wickplugin.ServiceRoute{{Prefix: prefix, Auth: wickplugin.AuthPublic}}},
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(pd, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInstallLoadsNewServiceWithoutReload: a service installed after boot
// is loaded and started, and an update swaps in its new manifest.
func TestInstallLoadsNewServiceWithoutReload(t *testing.T) {
	dir := t.TempDir()
	f := &fakeProcs{handler: http.NotFoundHandler()}
	host := NewHost(NewTokens(""), t.TempDir())
	host.spawn = f.spawn
	host.Start()
	t.Cleanup(host.StopAll)

	if host.Install(dir, "fresh", nil, nil) {
		t.Fatal("Install of a missing plugin reported true")
	}
	writeService(t, dir, "fresh", "1.0.0", "/a")
	if !host.Install(dir, "fresh", nil, nil) {
		t.Fatal("Install = false")
	}
	s, ok := host.Get("fresh")
	if !ok {
		t.Fatal("service not registered")
	}
	waitFor(t, "running", func() bool { return s.Sup.Status().State == StateRunning })

	writeService(t, dir, "fresh", "1.1.0", "/b")
	if !host.Install(dir, "fresh", nil, nil) {
		t.Fatal("update Install = false")
	}
	s2, _ := host.Get("fresh")
	if s2 == s || s2.Version != "1.1.0" || s2.Manifest.Routes[0].Prefix != "/b" {
		t.Fatalf("update not applied: %+v", s2)
	}
	waitFor(t, "running after update", func() bool { return s2.Sup.Status().State == StateRunning })
	if st := s.Sup.Status(); st.State != StateStopped {
		t.Fatalf("old process state = %s, want stopped", st.State)
	}
}

// TestInstallSkipsDisabled: a disabled service is not loaded.
func TestInstallSkipsDisabled(t *testing.T) {
	dir := t.TempDir()
	writeService(t, dir, "off", "1.0.0", "/")
	host := NewHost(NewTokens(""), t.TempDir())
	if host.Install(dir, "off", func(string) bool { return false }, nil) {
		t.Fatal("disabled service was installed")
	}
	if _, ok := host.Get("off"); ok {
		t.Fatal("disabled service registered")
	}
}
