package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connplugin "github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/plugins/source"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// writePlugin drops a minimal installed plugin (plugin.json only) under the
// kind dir, which is all Scan needs to list it.
func writePlugin(t *testing.T, kind, key, version string) {
	t.Helper()
	dir := filepath.Join(connplugin.KindDir(kind), key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"schema_version": 1, "kind": kind, "version": version, "entry": "bin",
		"module": map[string]any{"meta": map[string]any{"key": key, "name": strings.ToUpper(key)}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPluginsInstalledOriginPerPath: every install path shows its own
// origin, an unrecorded plugin is "local", and no URL carries a credential.
func TestPluginsInstalledOriginPerPath(t *testing.T) {
	sh := newSourcesHandler(t)
	db := sh.Sources.DB
	host := source.HostOSArch()
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"key": "loki", "version": "1.2.0", "assets": map[string]string{host: "https://dl.example.test/loki.zip?token=abc"}},
			{"key": "notion", "version": "1.0.0"},
		})
	}))
	defer cat.Close()
	t.Setenv("WICK_PLUGIN_CATALOG", cat.URL+"/plugins.json")

	h := NewPluginsHandler(db)
	h.sources = sh

	writePlugin(t, wickplugin.KindConnector, "loki", "1.0.0") // official, update 1.2.0
	writePlugin(t, wickplugin.KindTool, "pub", "1.0.0")       // public GitHub source
	writePlugin(t, wickplugin.KindJob, "priv", "2.0.0")       // private GitHub source
	writePlugin(t, wickplugin.KindService, "zipped", "0.1.0") // one-off zip link
	writePlugin(t, wickplugin.KindConnector, "uploaded", "1.0.0")
	writePlugin(t, wickplugin.KindTool, "handmade", "0.0.1") // copied in, no record

	now := time.Now()
	idx, _ := json.Marshal([]map[string]any{{"key": "pub", "kind": "tool", "version": "1.1.0",
		"assets": map[string]any{host: map[string]string{"url": "https://dl.example.test/pub-1.1.0.zip"}}}})
	for _, s := range []entity.PluginSource{
		{ID: "s-pub", Name: "Acme public", Type: source.TypeGitHub, Owner: "acme", Repo: "public", IndexJSON: string(idx), LastCheckAt: &now},
		{ID: "s-priv", Name: "Acme private", Type: source.TypeGitHub, Owner: "acme", Repo: "secret", Private: true, PAT: "wick_cenc_x"},
		{ID: "s-zip", Name: "Zip link", Type: source.TypeURL, URL: "https://user:pass@files.example.test/zipped-0.1.0.zip?sig=1"},
	} {
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, st := range []entity.PluginState{
		{Key: "loki", Kind: "connector", Enabled: true, Origin: connplugin.OriginOfficial},
		{Key: "pub", Kind: "tool", Enabled: false, SourceID: "s-pub", Origin: connplugin.OriginSource, AvailableVersion: "1.1.0"},
		{Key: "priv", Kind: "job", Enabled: true, SourceID: "s-priv"}, // claimed by Check, no origin recorded
		{Key: "zipped", Kind: "service", Enabled: true, SourceID: "s-zip", Origin: connplugin.OriginURLZip},
		{Key: "uploaded", Kind: "connector", Enabled: true, Origin: connplugin.OriginUpload},
	} {
		if err := db.Create(&st).Error; err != nil {
			t.Fatal(err)
		}
	}

	// gorm's default:true swallows Enabled:false on Create; disable explicitly.
	if err := db.Model(&entity.PluginState{}).Where("key = ?", "pub").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.apiInstalled(rec, httptest.NewRequest(http.MethodGet, "/manager/api/plugins/installed", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, leak := range []string{"token=", "user:pass", "sig=", "wick_cenc_"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaks %q: %s", leak, body)
		}
	}
	var resp installedListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Official.Plugins != 2 || resp.Official.URL == "" || resp.IsAdmin {
		t.Fatalf("official: %+v admin=%v", resp.Official, resp.IsAdmin)
	}
	got := map[string]installedPlugin{}
	for _, p := range resp.Plugins {
		got[p.Key] = p
	}
	if len(got) != 6 {
		t.Fatalf("want 6 installed, got %d: %+v", len(got), resp.Plugins)
	}
	check := func(key, origin, detail string) installedPlugin {
		t.Helper()
		p := got[key]
		if p.Origin != origin || p.DetailPath != detail {
			t.Errorf("%s: origin=%q detail=%q, want %q %q", key, p.Origin, p.DetailPath, origin, detail)
		}
		return p
	}
	if p := check("loki", "official", "/connectors/loki"); !p.UpdateAvailable || p.LatestVersion != "1.2.0" ||
		p.DownloadURL != "https://dl.example.test/loki.zip" || p.SourceName != "Official wick" {
		t.Errorf("loki: %+v", p)
	}
	if p := check("pub", "source", "/tools/pub"); p.Enabled || !p.UpdateAvailable || p.LatestVersion != "1.1.0" ||
		p.SourceURL != "https://github.com/acme/public" || p.DownloadURL != "https://dl.example.test/pub-1.1.0.zip" ||
		p.SourceName != "Acme public" || p.LastCheckAt == nil {
		t.Errorf("pub: %+v", p)
	}
	if p := check("priv", "source", "/jobs/priv"); p.SourceURL != "https://github.com/acme/secret" ||
		p.DownloadURL != "https://github.com/acme/secret/releases" || p.UpdateAvailable {
		t.Errorf("priv: %+v", p)
	}
	if p := check("zipped", "url-zip", "/services/zipped"); p.SourceURL != "https://files.example.test/zipped-0.1.0.zip" {
		t.Errorf("zipped: %+v", p)
	}
	if p := check("uploaded", "upload", "/connectors/uploaded"); p.SourceURL != "" || p.DownloadURL != "" {
		t.Errorf("uploaded: %+v", p)
	}
	if p := check("handmade", "local", "/tools/handmade"); !p.Enabled || p.SourceURL != "" || p.DownloadURL != "" {
		t.Errorf("handmade: %+v", p)
	}
}

func TestResolveOriginNeverGuesses(t *testing.T) {
	for _, c := range []struct {
		st   entity.PluginState
		want string
	}{
		{entity.PluginState{}, "local"},
		{entity.PluginState{Origin: "bogus"}, "local"},
		{entity.PluginState{Origin: "source"}, "local"}, // source unlinked (deleted) → unknown
		{entity.PluginState{Origin: "official"}, "official"},
		{entity.PluginState{SourceID: "x", Origin: "official"}, "source"},
		{entity.PluginState{SourceID: "x", Origin: "url-zip"}, "url-zip"},
	} {
		if got := resolveOrigin(c.st); got != c.want {
			t.Errorf("resolveOrigin(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
}

// TestPluginsInstalledSharedKeyKinds: a connector and a link tool share the
// key "loki". The connector's official origin and catalog update must not
// leak onto the tool, and the tool exposes its external link.
func TestPluginsInstalledSharedKeyKinds(t *testing.T) {
	sh := newSourcesHandler(t)
	db := sh.Sources.DB
	cat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"key": "loki", "version": "0.3.1"}})
	}))
	defer cat.Close()
	t.Setenv("WICK_PLUGIN_CATALOG", cat.URL+"/plugins.json")
	h := NewPluginsHandler(db)
	h.sources = sh

	writePlugin(t, wickplugin.KindConnector, "loki", "0.3.0")
	writePlugin(t, wickplugin.KindTool, "loki", "0.1.0")
	toolJSON := filepath.Join(connplugin.KindDir(wickplugin.KindTool), "loki", "plugin.json")
	m := map[string]any{"schema_version": 1, "kind": "tool", "version": "0.1.0", "entry": "bin",
		"module": map[string]any{"meta": map[string]any{"key": "loki", "name": "Loki"}},
		"tool":   map[string]any{"meta": map[string]any{"key": "loki", "name": "Loki", "external_url": "https://loki.example.test/"}}}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(toolJSON, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&entity.PluginState{Key: "loki", Kind: "connector", Enabled: true, Origin: connplugin.OriginOfficial}).Error; err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.apiInstalled(rec, httptest.NewRequest(http.MethodGet, "/manager/api/plugins/installed", nil))
	var resp installedListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	got := map[string]installedPlugin{}
	for _, p := range resp.Plugins {
		got[p.Kind] = p
	}
	if c := got["connector"]; c.Origin != "official" || !c.UpdateAvailable || c.ExternalURL != "" {
		t.Errorf("connector: %+v", c)
	}
	if tl := got["tool"]; tl.Origin != "local" || tl.UpdateAvailable || tl.LatestVersion != "" || tl.ExternalURL != "https://loki.example.test/" {
		t.Errorf("tool: %+v", tl)
	}
}
