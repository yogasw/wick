package app

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

func makeSrc(t *testing.T, key, content string, arch string) string {
	t.Helper()
	src := t.TempDir()
	bin := filepath.Join(src, key)
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(content))
	env := wickplugin.Manifest{
		SchemaVersion: 1, Version: "1.0.0", ProtoVersion: wickplugin.ProtoVersion,
		Entry: key, OSArch: []string{arch}, SHA256: hex.EncodeToString(h[:]),
		Module: connector.Module{Meta: connector.Meta{Key: key, Name: key}},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestInstallFromDir(t *testing.T) {
	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "0")
	t.Setenv("WICK_PLUGIN_PUBKEY", "")
	host := runtime.GOOS + "/" + runtime.GOARCH
	src := makeSrc(t, "demo", "binbytes", host)
	dest := t.TempDir()

	if err := installPlugin(src, dest); err != nil {
		t.Fatalf("install should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "demo", "demo")); err != nil {
		t.Fatal("binary not installed")
	}
	if _, err := os.Stat(filepath.Join(dest, "demo", "plugin.json")); err != nil {
		t.Fatal("manifest not installed")
	}
}

func TestInstallRejectsWrongArch(t *testing.T) {
	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "0")
	src := makeSrc(t, "demo", "binbytes", "plan9/foo")
	dest := t.TempDir()
	if err := installPlugin(src, dest); err == nil {
		t.Fatal("install must reject wrong-arch plugin")
	}
}
