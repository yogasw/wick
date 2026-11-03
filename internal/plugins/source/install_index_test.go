package source

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// catalogServer serves a plugins.json listing one plugin plus its zip. asset
// shapes the os/arch entry (a bare URL in the official catalog).
func catalogServer(t *testing.T, key, zipKind, indexKind string, asset func(url, zipSum string) any) string {
	t.Helper()
	zb, zs := buildZip(t, key, zipKind, "0.1.0")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			_, _ = w.Write(zb)
			return
		}
		e := map[string]any{"key": key, "version": "0.1.0",
			"assets": map[string]any{HostOSArch(): asset("files/"+zipName(key, "0.1.0"), zs)}}
		if indexKind != "" {
			e["kind"] = indexKind
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{e})
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/plugins.json"
}

func bareURL(url, _ string) any { return url }

func indexManager(t *testing.T, hooks *[]string) *Manager {
	return &Manager{DB: newDB(t), Client: &Client{HTTP: http.DefaultClient}, Install: tmpRoot(t),
		OnInstalled: func(_ context.Context, kind, key string) { *hooks = append(*hooks, kind+":"+key) }}
}

// TestInstallFromIndexOfficialCatalog: a kind-less v1 entry installs as the
// wanted kind, is recorded with the official origin and runs OnInstalled.
func TestInstallFromIndexOfficialCatalog(t *testing.T) {
	var hooks []string
	m := indexManager(t, &hooks)
	u := catalogServer(t, "runner_x", "service", "", bareURL)
	in, err := m.InstallFromIndex(context.Background(), u, "runner_x", wickplugin.KindService, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if in.Key != "runner_x" || in.Kind != "service" || in.Version != "0.1.0" {
		t.Fatalf("installed: %+v", in)
	}
	if _, err := os.Stat(filepath.Join(m.Install.Root("service"), "runner_x", "runner_x")); err != nil {
		t.Fatalf("binary missing: %v", err)
	}
	var st entity.PluginState
	m.DB.Where("key = ?", "runner_x").First(&st)
	if st.Origin != "official" || st.Kind != "service" || st.InstalledVersion != "0.1.0" || !st.Enabled {
		t.Fatalf("state: %+v", st)
	}
	if strings.Join(hooks, ",") != "service:runner_x" {
		t.Fatalf("hooks: %v", hooks)
	}
	if audit, _ := m.Audit(1); len(audit) != 1 || audit[0].Action != "install" || audit[0].Actor != "auto" {
		t.Fatalf("audit: %+v", audit)
	}
}

func TestInstallFromIndexMissingKey(t *testing.T) {
	var hooks []string
	m := indexManager(t, &hooks)
	u := catalogServer(t, "other", "service", "", bareURL)
	if _, err := m.InstallFromIndex(context.Background(), u, "runner_x", wickplugin.KindService, "auto"); !errors.Is(err, ErrNotInIndex) {
		t.Fatalf("err = %v", err)
	}
	// Listed under another kind counts as missing too.
	u = catalogServer(t, "runner_x", "connector", "connector", bareURL)
	if _, err := m.InstallFromIndex(context.Background(), u, "runner_x", wickplugin.KindService, "auto"); !errors.Is(err, ErrNotInIndex) {
		t.Fatalf("kind mismatch err = %v", err)
	}
	if len(hooks) != 0 {
		t.Fatalf("hooks ran: %v", hooks)
	}
}

// TestInstallFromIndexChecks: the manifest kind and the zip_sha256 are
// enforced before anything lands on disk.
func TestInstallFromIndexChecks(t *testing.T) {
	var hooks []string
	m := indexManager(t, &hooks)
	u := catalogServer(t, "runner_x", "connector", "", bareURL)
	if _, err := m.InstallFromIndex(context.Background(), u, "runner_x", wickplugin.KindService, "auto"); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatalf("connector zip accepted as service: %v", err)
	}
	u = catalogServer(t, "runner_x", "service", "service", func(url, _ string) any {
		return map[string]string{"url": url, "zip_sha256": strings.Repeat("0", 64)}
	})
	if _, err := m.InstallFromIndex(context.Background(), u, "runner_x", wickplugin.KindService, "auto"); err == nil || !strings.Contains(err.Error(), "zip_sha256 mismatch") {
		t.Fatalf("bad zip_sha256 accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Install.Root("service"), "runner_x")); !os.IsNotExist(err) {
		t.Fatal("rejected plugin landed on disk")
	}
	if audit, _ := m.Audit(5); len(audit) != 2 || audit[0].Action != "install.fail" {
		t.Fatalf("audit: %+v", audit)
	}
	if len(hooks) != 0 {
		t.Fatalf("hooks ran: %v", hooks)
	}
}
