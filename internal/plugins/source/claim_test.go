package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestCheckClaimsPluginWithoutStateRow: a plugin copied in by hand has no
// plugin_states row. Check must create one pointing at the source (with the
// newer version flagged), keep an existing row's enabled flag, and leave a
// row owned by another source alone.
func TestCheckClaimsPluginWithoutStateRow(t *testing.T) {
	ver := "1.0.0"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			zb, _ := buildZip(t, "echo", "service", strings.Split(filepath.Base(r.URL.Path), "-")[1])
			_, _ = w.Write(zb)
			return
		}
		_, zs := buildZip(t, "echo", "service", ver)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"key": "echo", "kind": "service", "version": ver,
			"assets": map[string]any{HostOSArch(): map[string]string{"url": "files/" + zipName("echo", ver), "zip_sha256": zs}}}})
	}))
	defer srv.Close()

	ctx := context.Background()
	m := &Manager{DB: newDB(t), Client: &Client{HTTP: http.DefaultClient}, Install: tmpRoot(t),
		Encrypt: func(p string) (string, error) { return "wick_cenc_" + p, nil }}
	s, err := m.Save("", SourceInput{Type: TypeURL, URL: srv.URL + "/plugins.json"}, "admin@x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Check(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	// Put v1.0.0 on disk, then drop its state row: what a hand copy looks like.
	if _, err := m.InstallFromSource(ctx, s.ID, "echo", "admin@x", nil); err != nil {
		t.Fatal(err)
	}
	m.DB.Where("key = ?", "echo").Delete(&entity.PluginState{})

	ver = "1.1.0"
	res, err := m.Check(ctx, s.ID)
	if err != nil || len(res.Updates) != 1 {
		t.Fatalf("update not detected: %+v %v", res, err)
	}
	var st entity.PluginState
	if err := m.DB.Where("key = ?", "echo").First(&st).Error; err != nil {
		t.Fatalf("no state row after check: %v", err)
	}
	if st.SourceID != s.ID || st.Origin != "source" || st.AvailableVersion != "1.1.0" || !st.Enabled || st.Kind != "service" || st.InstalledVersion != "1.0.0" {
		t.Fatalf("claimed state: %+v", st)
	}

	// An existing unclaimed, disabled row keeps enabled=false.
	m.DB.Model(&entity.PluginState{}).Where("key = ?", "echo").
		Updates(map[string]any{"enabled": false, "source_id": "", "origin": "", "available_version": ""})
	if _, err := m.Check(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	m.DB.Where("key = ?", "echo").First(&st)
	if st.Enabled || st.SourceID != s.ID || st.Origin != "source" || st.AvailableVersion != "1.1.0" {
		t.Fatalf("disabled row after check: %+v", st)
	}

	// A row owned by another source is not taken.
	m.DB.Model(&entity.PluginState{}).Where("key = ?", "echo").
		Updates(map[string]any{"source_id": "other", "available_version": ""})
	if _, err := m.Check(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	m.DB.Where("key = ?", "echo").First(&st)
	if st.SourceID != "other" || st.AvailableVersion != "" {
		t.Fatalf("other source's row changed: %+v", st)
	}
}
