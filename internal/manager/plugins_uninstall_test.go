package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/plugins/source"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func removeReq(h *PluginsHandler, key, kind string) *httptest.ResponseRecorder {
	target := "/manager/api/plugins/" + key + "/remove"
	if kind != "" {
		target += "?kind=" + kind
	}
	req := httptest.NewRequest(http.MethodPost, target, nil)
	req.SetPathValue("key", key)
	rec := httptest.NewRecorder()
	h.apiRemove(rec, req)
	return rec
}

// TestPluginsUninstallEveryKind: Uninstall finds the plugin in any kind
// folder, unloads it before the files go, clears the recorded versions and
// keeps the row; unknown keys are 404 and built-ins are refused.
func TestPluginsUninstallEveryKind(t *testing.T) {
	sh := newSourcesHandler(t)
	db := sh.Sources.DB
	h := NewPluginsHandler(db)

	var unloaded []string
	h.SetUninstall(func(_ context.Context, kind, key string) {
		// The service is stopped while its files still exist.
		if _, err := os.Stat(filepath.Join(connplugin.KindDir(kind), key)); err != nil {
			t.Errorf("unload %s/%s ran after the files were deleted", kind, key)
		}
		unloaded = append(unloaded, kind+":"+key)
	}, func(key string) bool { return key == "builtin-http" })

	for _, kind := range wickplugin.Kinds {
		key := "p-" + kind
		writePlugin(t, kind, key, "1.0.0")
		if err := h.store.Record(key, kind, "1.0.0"); err != nil {
			t.Fatal(err)
		}
		db.Model(&entity.PluginState{}).Where("key = ?", key).Updates(map[string]any{"available_version": "1.1.0", "origin": connplugin.OriginSource})

		if rec := removeReq(h, key, ""); rec.Code != http.StatusOK {
			t.Fatalf("remove %s: %d %s", key, rec.Code, rec.Body.String())
		}
		if _, err := os.Stat(filepath.Join(connplugin.KindDir(kind), key)); !os.IsNotExist(err) {
			t.Fatalf("%s folder still there (err=%v)", key, err)
		}
		st, ok := h.store.Get(key)
		if !ok || st.InstalledVersion != "" || st.AvailableVersion != "" || st.Origin != connplugin.OriginSource {
			t.Fatalf("%s state not cleared (row kept): ok=%v %+v", key, ok, st)
		}
	}
	if len(unloaded) != len(wickplugin.Kinds) {
		t.Fatalf("unload calls = %v", unloaded)
	}

	if rec := removeReq(h, "nope", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown key: %d", rec.Code)
	}
	if rec := removeReq(h, "builtin-http", ""); rec.Code != http.StatusConflict {
		t.Fatalf("built-in: want 409, got %d", rec.Code)
	}

	// A connector and a tool sharing a key: ?kind= picks the tool only.
	writePlugin(t, wickplugin.KindConnector, "twin", "1.0.0")
	writePlugin(t, wickplugin.KindTool, "twin", "1.0.0")
	if rec := removeReq(h, "twin", "tool"); rec.Code != http.StatusOK {
		t.Fatalf("remove twin tool: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(connplugin.KindDir(wickplugin.KindConnector), "twin")); err != nil {
		t.Fatalf("connector twin removed too: %v", err)
	}
}

// TestPluginAvailableKeepsInstalled: Marketplace rows stay listed once
// installed, carrying installed_version; a disabled source is not listed.
func TestPluginAvailableKeepsInstalled(t *testing.T) {
	h := newSourcesHandler(t)
	host := source.HostOSArch()
	idx, _ := json.Marshal([]map[string]any{
		{"key": "pub", "kind": "tool", "version": "1.1.0", "assets": map[string]any{host: map[string]string{"url": "https://dl.example.test/pub.zip"}}},
		{"key": "fresh", "kind": "job", "version": "0.1.0", "assets": map[string]any{}},
	})
	for _, s := range []entity.PluginSource{
		{ID: "s-on", Name: "On", Type: source.TypeURL, URL: "https://x.example.test/plugins.json", IndexJSON: string(idx)},
		{ID: "s-off", Name: "Off", Type: source.TypeURL, URL: "https://y.example.test/plugins.json", IndexJSON: string(idx)},
	} {
		if err := h.Sources.DB.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	h.Sources.DB.Model(&entity.PluginSource{}).Where("id = ?", "s-off").Update("enabled", false)
	writePlugin(t, wickplugin.KindTool, "pub", "1.0.0")

	rec := httptest.NewRecorder()
	h.apiAvailable(rec, httptest.NewRequest(http.MethodGet, "/manager/api/plugin-available", nil))
	var out struct {
		Available []availableView `json:"available"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]availableView{}
	for _, a := range out.Available {
		if a.SourceID != "s-on" {
			t.Fatalf("disabled source listed: %+v", a)
		}
		got[a.Key] = a
	}
	if len(got) != 2 || got["pub"].InstalledVersion != "1.0.0" || !got["pub"].ArchOK || got["fresh"].InstalledVersion != "" || got["fresh"].ArchOK {
		t.Fatalf("available = %+v", out.Available)
	}
}
