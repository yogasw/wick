package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func writeDemoPlugin(t *testing.T, dir, key, content string) {
	t.Helper()
	cdir := filepath.Join(dir, key)
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(cdir, key)
	if err := os.WriteFile(bin, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(content))
	env := wickplugin.Manifest{
		SchemaVersion: 1, Version: "t", ProtoVersion: wickplugin.ProtoVersion,
		Entry: key, OSArch: []string{runtime.GOOS + "/" + runtime.GOARCH},
		SHA256: hex.EncodeToString(h[:]),
		Module: connector.Module{Meta: connector.Meta{Key: key, Name: key}},
	}
	b, _ := json.Marshal(env)
	if err := os.WriteFile(filepath.Join(cdir, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeUpserter struct {
	upserted map[string]bool
	removed  map[string]bool
}

func (f *fakeUpserter) UpsertModule(_ context.Context, m connector.Module) error {
	f.upserted[m.Meta.Key] = true
	return nil
}
func (f *fakeUpserter) RemoveModule(key string) { f.removed[key] = true }

func TestReloaderReconcile(t *testing.T) {
	t.Setenv("WICK_PLUGIN_REQUIRE_SIGNATURE", "0")
	t.Setenv("WICK_PLUGIN_PUBKEY", "")
	dir := t.TempDir()
	writeDemoPlugin(t, dir, "alpha", "v1")

	svc := &fakeUpserter{upserted: map[string]bool{}, removed: map[string]bool{}}
	mgr := &Manager{entries: map[string]*entry{}, binaries: map[string]string{}, now: func() time.Time { return time.Unix(0, 0) }, stop: make(chan struct{})}
	mgr.killFn = mgr.kill
	r := &Reloader{dir: dir, svc: svc, mgr: mgr, seen: map[string]string{}}

	/* first reconcile: alpha is new → upserted + binary set */
	r.reconcile(context.Background())
	if !svc.upserted["alpha"] || !mgr.IsPlugin("alpha") {
		t.Fatalf("alpha should be added: %+v / isPlugin=%v", svc.upserted, mgr.IsPlugin("alpha"))
	}

	/* add beta, remove alpha → beta upserted, alpha removed */
	writeDemoPlugin(t, dir, "beta", "b1")
	os.RemoveAll(filepath.Join(dir, "alpha"))
	svc.upserted = map[string]bool{}
	r.reconcile(context.Background())
	if !svc.upserted["beta"] {
		t.Fatal("beta should be added on second reconcile")
	}
	if !svc.removed["alpha"] {
		t.Fatal("alpha should be removed on second reconcile")
	}
	if mgr.IsPlugin("alpha") {
		t.Fatal("alpha binary should be dropped")
	}
}
