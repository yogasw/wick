package userconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/appname"
)

func providersHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(appname.DataDirEnv, dir)
	return dir
}

func writeRawConfig(t *testing.T, dir string, cfg Config) {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A data dir an older version wrote (providers inside config.json) moves
// to providers/configs on first read, one file per instance, order kept.
func TestProvidersMigrateFromConfigJSON(t *testing.T) {
	dir := providersHome(t)
	writeRawConfig(t, dir, Config{Port: 9425,
		Providers: ProvidersConfig{Claude: []ProviderInstance{
			{Name: "claude_waba", Env: []string{"CLAUDE_CONFIG_DIR=/x"}},
			{Name: "enginer", ExtraArgs: []string{"--verbose"}},
		}},
		ProviderStatuses: map[string]ProviderStatus{"claude/enginer": {Version: "2.1"}},
	})
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers.Claude) != 2 || cfg.Providers.Claude[0].Name != "claude_waba" || cfg.Providers.Claude[1].Name != "enginer" {
		t.Fatalf("claude = %+v", cfg.Providers.Claude)
	}
	for _, f := range []string{"providers/configs/claude-claude_waba.json", "providers/configs/claude-enginer.json", "providers/configs/.migrated", "providers/status/claude-enginer.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if cfg.ProviderStatuses["claude/enginer"].Version != "2.1" {
		t.Fatalf("statuses = %+v", cfg.ProviderStatuses)
	}
}

// The bug this layout fixes: a writer holding an older copy of the whole
// config (the updater, the System page) saves its own field, and the
// providers saved meanwhile stay as they are.
func TestStaleSaveKeepsProviders(t *testing.T) {
	dir := providersHome(t)
	writeRawConfig(t, dir, Config{Providers: ProvidersConfig{Claude: []ProviderInstance{{Name: "claude_waba"}}}})
	stale, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := Load("")
	fresh.Providers.Claude[0].Env = []string{"CLAUDE_CONFIG_DIR=/home/u/.claude-waba"}
	fresh.Providers.Claude[0].Models = []UserModelEntry{{ID: "opus"}}
	if err := SaveProviders("", fresh.Providers); err != nil {
		t.Fatal(err)
	}
	stale.AutoUpdate = true
	if err := Save("", stale); err != nil {
		t.Fatal(err)
	}
	got, _ := Load("")
	if !got.AutoUpdate {
		t.Fatal("the stale writer's own field was not saved")
	}
	c := got.Providers.Claude
	if len(c) != 1 || len(c[0].Env) != 1 || len(c[0].Models) != 1 {
		t.Fatalf("providers reverted by a stale Save: %+v", c)
	}
	// config.json mirrors the files for an older binary.
	raw, _ := loadFile("")
	if len(raw.Providers.Claude) != 1 || len(raw.Providers.Claude[0].Env) != 1 {
		t.Fatalf("mirror = %+v", raw.Providers.Claude)
	}
}

// Removing an instance removes its file; an unchanged one is not rewritten.
func TestSaveProvidersRemovesAndSkipsUnchanged(t *testing.T) {
	dir := providersHome(t)
	pc := ProvidersConfig{Claude: []ProviderInstance{{Name: "a"}, {Name: "b"}}}
	if err := SaveProviders("", pc); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dir, "providers/configs/claude-a.json")
	st, _ := os.Stat(a)
	pc.Claude = pc.Claude[:1]
	if err := SaveProviders("", pc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "providers/configs/claude-b.json")); !os.IsNotExist(err) {
		t.Fatalf("b still on disk: %v", err)
	}
	st2, _ := os.Stat(a)
	if !st.ModTime().Equal(st2.ModTime()) {
		t.Fatal("unchanged instance rewritten")
	}
	got, _ := Load("")
	if len(got.Providers.Claude) != 1 || got.Providers.Claude[0].Name != "a" {
		t.Fatalf("claude = %+v", got.Providers.Claude)
	}
}
