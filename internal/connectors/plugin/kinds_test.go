package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func TestKindDirLayout(t *testing.T) {
	t.Setenv("WICK_PLUGINS_ROOT", "/srv/plugins")
	t.Setenv("WICK_PLUGINS_DIR", "")
	want := map[string]string{
		wickplugin.KindConnector: "/srv/plugins/connectors",
		wickplugin.KindJob:       "/srv/plugins/jobs",
		wickplugin.KindTool:      "/srv/plugins/tools",
		wickplugin.KindService:   "/srv/plugins/services",
	}
	for kind, dir := range want {
		if got := KindDir(kind); got != dir {
			t.Fatalf("KindDir(%s) = %s, want %s", kind, got, dir)
		}
	}
	// The connector dir keeps its own override.
	t.Setenv("WICK_PLUGINS_DIR", "/legacy/connectors")
	if got := KindDir(wickplugin.KindConnector); got != "/legacy/connectors" {
		t.Fatalf("connector override ignored: %s", got)
	}
}

func writeKindManifest(t *testing.T, dir, key, kind string) {
	t.Helper()
	pdir := filepath.Join(dir, key)
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(wickplugin.Manifest{
		SchemaVersion: 1, Kind: kind, Entry: key,
		Module: connector.Module{Meta: connector.Meta{Key: key}},
	})
	if err := os.WriteFile(filepath.Join(pdir, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanKindRejectsForeignKind(t *testing.T) {
	dir := t.TempDir()
	writeKindManifest(t, dir, "nightly", wickplugin.KindJob)
	writeKindManifest(t, dir, "slack", wickplugin.KindConnector)
	writeKindManifest(t, dir, "legacy", "") // pre-kind manifest = connector

	jobs, err := ScanKind(dir, wickplugin.KindJob)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Key != "nightly" {
		t.Fatalf("job scan = %+v, want only nightly", jobs)
	}
	conns, err := ScanKind(dir, wickplugin.KindConnector)
	if err != nil {
		t.Fatal(err)
	}
	if len(conns) != 2 {
		t.Fatalf("connector scan = %+v, want slack + legacy", conns)
	}
}

func TestPluginStateMigrateIsAdditive(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// An old table: only the pre-kind columns, with one disabled row.
	if err := db.Exec(`CREATE TABLE plugin_states (key text PRIMARY KEY, enabled numeric DEFAULT true, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO plugin_states (key, enabled) VALUES ('slack', false)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.PluginState{}); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"kind", "source_id", "installed_version", "available_version", "last_health_at", "last_health_ok", "last_health_detail"} {
		if !db.Migrator().HasColumn(&entity.PluginState{}, col) {
			t.Fatalf("migrate should add column %s", col)
		}
	}
	s := NewStateStore(db)
	if s.Enabled("slack") {
		t.Fatal("existing disabled row must survive the migration")
	}
	st, ok := s.Get("slack")
	if !ok || st.Kind != wickplugin.KindConnector {
		t.Fatalf("old row should read back as connector, got %+v ok=%v", st, ok)
	}
}

func TestStateStoreRecordKeepsEnabled(t *testing.T) {
	s := NewStateStore(newStateDB(t))
	if err := s.Record("nightly", wickplugin.KindJob, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if !s.Enabled("nightly") {
		t.Fatal("a recorded plugin starts enabled")
	}
	_ = s.SetEnabled("nightly", false)
	if err := s.Record("nightly", wickplugin.KindJob, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Get("nightly")
	if st.Enabled || st.Kind != wickplugin.KindJob || st.InstalledVersion != "1.1.0" {
		t.Fatalf("Record must keep the enable flag and update kind/version, got %+v", st)
	}
}
