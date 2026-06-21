package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func TestScanFindsManifests(t *testing.T) {
	found, err := Scan("testdata/connectors")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 manifest, got %d", len(found))
	}
	if found[0].Key != "demo" {
		t.Fatalf("key not parsed: %s", found[0].Key)
	}
	if found[0].Manifest.Entry != "demo" {
		t.Fatalf("entry not parsed: %s", found[0].Manifest.Entry)
	}
	if found[0].BinaryPath == "" {
		t.Fatal("binary path must be set (sibling of plugin.json)")
	}
}

func TestLoadRegistersModules(t *testing.T) {
	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "0")
	t.Setenv("WICK_PLUGIN_PUBKEY", "")
	dir := t.TempDir()
	cdir := filepath.Join(dir, "demo")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(cdir, "demo")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	env := wickplugin.Manifest{
		SchemaVersion: 1,
		Version:       "t",
		ProtoVersion:  wickplugin.ProtoVersion,
		Entry:         "demo",
		OSArch:        []string{runtime.GOOS + "/" + runtime.GOARCH},
		SHA256:        hex.EncodeToString(h[:]),
		Module: connector.Module{
			Meta: connector.Meta{Key: "demo", Name: "Demo"},
			Operations: []connector.Category{
				{Title: "Main", Ops: []connector.Operation{
					{Key: "say", Name: "Say", Description: "echo"},
				}},
			},
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cdir, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	var registered []connector.Module
	register := func(m connector.Module) { registered = append(registered, m) }

	n, err := loadWith(dir, register, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(registered) != 1 || registered[0].Meta.Key != "demo" {
		t.Fatalf("expected demo registered, got %d / %+v", n, registered)
	}
}
