package cli

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/plugins/source"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// writeReleaseZip writes a zip holding only plugin.json — enough for the
// index, which never opens the binary.
func writeReleaseZip(t *testing.T, dir, key, kind, version, osArch string) string {
	t.Helper()
	m := wickplugin.Manifest{
		SchemaVersion: wickplugin.ManifestSchemaVersion,
		Kind:          kind,
		Version:       version,
		ProtoVersion:  1,
		Entry:         key,
		OSArch:        []string{osArch},
		Module:        connector.Module{Meta: connector.Meta{Key: key, Name: strings.ToUpper(key), Description: "d"}},
	}
	raw, _ := json.Marshal(m)
	p := filepath.Join(dir, key+"-"+version+"-"+strings.ReplaceAll(osArch, "/", "-")+".zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("plugin.json")
	_, _ = w.Write(raw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func TestBuildPluginIndex(t *testing.T) {
	dir := t.TempDir()
	amd := writeReleaseZip(t, dir, "auto_get_data", "job", "0.2.0", "linux/amd64")
	writeReleaseZip(t, dir, "auto_get_data", "job", "0.2.0", "linux/arm64")
	writeReleaseZip(t, dir, "text_counter", "tool", "1.0.0", "linux/amd64")

	entries, err := buildPluginIndex(dir, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Key != "auto_get_data" || entries[1].Key != "text_counter" {
		t.Fatalf("entries = %+v", entries)
	}
	e := entries[0]
	if e.Kind != "job" || e.Version != "0.2.0" || e.Name != "AUTO_GET_DATA" || len(e.Assets) != 2 {
		t.Fatalf("entry = %+v", e)
	}
	raw, _ := os.ReadFile(amd)
	sum := sha256.Sum256(raw)
	a := e.Assets["linux/amd64"]
	if a.URL != "auto_get_data-0.2.0-linux-amd64.zip" || a.ZipSHA256 != hex.EncodeToString(sum[:]) || a.Signature != "" {
		t.Fatalf("asset = %+v", a)
	}

	// The written index must round-trip through the host parser, resolving
	// the relative URL against where plugins.json was fetched from.
	data, _ := json.Marshal(entries)
	parsed, err := source.ParseIndex(data, "https://example.com/rel/plugins.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed[0].Assets["linux/amd64"].URL; got != "https://example.com/rel/auto_get_data-0.2.0-linux-amd64.zip" {
		t.Fatalf("resolved url = %s", got)
	}

	only, err := buildPluginIndex(dir, "https://cdn.example.com/p/", "", []string{"text_counter"})
	if err != nil || len(only) != 1 || only[0].Assets["linux/amd64"].URL != "https://cdn.example.com/p/text_counter-1.0.0-linux-amd64.zip" {
		t.Fatalf("filtered = %+v, %v", only, err)
	}
}

func TestBuildPluginIndex_Rejects(t *testing.T) {
	dir := t.TempDir()
	writeReleaseZip(t, dir, "x", "tool", "1.0.0", "linux/amd64")
	writeReleaseZip(t, dir, "x", "tool", "1.1.0", "linux/arm64")
	if _, err := buildPluginIndex(dir, "", "", nil); err == nil || !strings.Contains(err.Error(), "two versions") {
		t.Fatalf("want two-versions error, got %v", err)
	}

	dir2 := t.TempDir()
	p := writeReleaseZip(t, dir2, "y", "tool", "1.0.0", "linux/amd64")
	if err := os.Rename(p, filepath.Join(dir2, "y-9.9.9-linux-amd64.zip")); err != nil {
		t.Fatal(err)
	}
	if _, err := buildPluginIndex(dir2, "", "", nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("want name mismatch error, got %v", err)
	}

	if _, err := buildPluginIndex(t.TempDir(), "", "", nil); err == nil {
		t.Fatal("empty dir must fail")
	}
}

func TestSignIndexFile(t *testing.T) {
	dir := t.TempDir()
	writeReleaseZip(t, dir, "svc", "service", "0.1.0", "linux/amd64")
	priv, pub := wickplugin.GenerateKeypair()
	keyPath := filepath.Join(dir, "k")
	if err := os.WriteFile(keyPath, []byte(priv), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := buildPluginIndex(dir, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(entries)
	idx := filepath.Join(dir, "plugins.json")
	if err := os.WriteFile(idx, data, 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := signIndexFile(idx, keyPath)
	if err != nil || n != 1 {
		t.Fatalf("sign: n=%d err=%v", n, err)
	}
	raw, _ := os.ReadFile(idx)
	var signed []source.Entry
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatal(err)
	}
	a := signed[0].Assets["linux/amd64"]
	if !wickplugin.VerifySHA256([]string{pub}, a.ZipSHA256, a.Signature) {
		t.Fatal("signature does not verify against the public key")
	}
	// index --sign-key produces the same verifiable signature.
	direct, err := buildPluginIndex(dir, "", keyPath, nil)
	if err != nil || !wickplugin.VerifySHA256([]string{pub}, direct[0].Assets["linux/amd64"].ZipSHA256, direct[0].Assets["linux/amd64"].Signature) {
		t.Fatalf("index --sign-key: %v", err)
	}
}
