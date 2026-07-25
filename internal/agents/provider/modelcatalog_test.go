package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// resetModelCatalog clears the in-memory merge so the next SeedModels call
// rebuilds from the current embedded + disk state. Tests that touch the disk
// cache or clock must call this to avoid a stale build leaking across cases.
func resetModelCatalog() {
	modelCatMu.Lock()
	modelCatMerged = nil
	modelCatBuiltAt = time.Time{}
	modelCatMu.Unlock()
}

// isolateHome points userconfig.Dir at a temp dir so disk-cache reads/writes
// don't touch the real home, and returns the cache file path.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	p, err := modelCachePath()
	if err != nil {
		t.Fatalf("modelCachePath: %v", err)
	}
	return p
}

func writeDiskCatalog(t *testing.T, path string, cat modelCatalog) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b, _ := json.MarshalIndent(cat, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write disk catalog: %v", err)
	}
}

// TestSeedModels_EmbeddedBaseline verifies the embedded models.json seeds the
// three CLI types and leaves wick empty, with no disk cache present.
func TestSeedModels_EmbeddedBaseline(t *testing.T) {
	isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	for _, tt := range []struct {
		typ  Type
		want bool
	}{
		{TypeClaude, true},
		{TypeCodex, true},
		{TypeGemini, true},
		{TypeWick, false},
	} {
		got := SeedModels(tt.typ)
		if (len(got) > 0) != tt.want {
			t.Errorf("%s: seed present=%v want %v (%v)", tt.typ, len(got) > 0, tt.want, got)
		}
	}
	// Embedded seeds carry descriptions.
	if s := SeedModels(TypeClaude); len(s) == 0 || s[0].Desc == "" {
		t.Errorf("claude seed should carry desc, got %+v", s)
	}
	// Codex uses the current gpt-5.5 line.
	if s := SeedModels(TypeCodex); len(s) == 0 || s[0].ID != "gpt-5.5" {
		t.Errorf("codex first seed should be gpt-5.5, got %+v", s)
	}
}

// TestBuildMerged_DiskOverlayNewerWins verifies a disk-cached (remote) entry
// with a newer updated_at overrides the embedded one, and a remote-only id is
// appended.
func TestBuildMerged_DiskOverlayNewerWins(t *testing.T) {
	path := isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	writeDiskCatalog(t, path, modelCatalog{
		UpdatedAt: "2099-01-01T00:00:00Z",
		Providers: map[string][]modelEntry{
			"claude": {
				// Same id as embedded, newer → its desc wins.
				{ID: "opus", Desc: "REMOTE opus", Enabled: true, UpdatedAt: "2099-01-01T00:00:00Z"},
				// Remote-only new model → appended.
				{ID: "opus-next", Desc: "brand new", Enabled: true, UpdatedAt: "2099-01-01T00:00:00Z"},
			},
		},
	})

	got := SeedModels(TypeClaude)
	byID := map[string]string{}
	for _, s := range got {
		byID[s.ID] = s.Desc
	}
	if byID["opus"] != "REMOTE opus" {
		t.Errorf("newer remote opus desc should win, got %q", byID["opus"])
	}
	if _, ok := byID["opus-next"]; !ok {
		t.Error("remote-only model opus-next should be added")
	}
	// Embedded-only models survive.
	if _, ok := byID["sonnet"]; !ok {
		t.Error("embedded-only sonnet should still be present")
	}
}

// TestBuildMerged_OlderRemoteLoses verifies an older remote entry does NOT
// override a newer embedded one.
func TestBuildMerged_OlderRemoteLoses(t *testing.T) {
	path := isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	writeDiskCatalog(t, path, modelCatalog{
		Providers: map[string][]modelEntry{
			"claude": {
				{ID: "opus", Desc: "STALE opus", Enabled: true, UpdatedAt: "2000-01-01T00:00:00Z"},
			},
		},
	})
	for _, s := range SeedModels(TypeClaude) {
		if s.ID == "opus" && s.Desc == "STALE opus" {
			t.Error("older remote opus should not override the newer embedded desc")
		}
	}
}

// TestBuildMerged_DisabledHidden verifies a model flipped to enabled=false by
// a newer remote entry disappears from the picker.
func TestBuildMerged_DisabledHidden(t *testing.T) {
	path := isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	writeDiskCatalog(t, path, modelCatalog{
		Providers: map[string][]modelEntry{
			"claude": {
				{ID: "haiku", Desc: "gone", Enabled: false, UpdatedAt: "2099-01-01T00:00:00Z"},
			},
		},
	})
	for _, s := range SeedModels(TypeClaude) {
		if s.ID == "haiku" {
			t.Error("remote-disabled haiku should be hidden from the picker")
		}
	}
}

// TestRefreshRemoteModels_FetchAndPersist verifies a manual refresh fetches
// from the (stubbed) remote, persists to disk, and rebuilds the merge so the
// new model is served.
func TestRefreshRemoteModels_FetchAndPersist(t *testing.T) {
	path := isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(modelCatalog{
			UpdatedAt: "2099-01-01T00:00:00Z",
			Providers: map[string][]modelEntry{
				"claude": {{ID: "opus-remote", Desc: "from server", Enabled: true, UpdatedAt: "2099-01-01T00:00:00Z"}},
			},
		})
	}))
	defer srv.Close()

	oldURL := remoteModelsURL
	remoteModelsURL = srv.URL
	defer func() { remoteModelsURL = oldURL }()

	if err := RefreshRemoteModels(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// Disk cache written.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	// Merge now includes the remote-only model.
	found := false
	for _, s := range SeedModels(TypeClaude) {
		if s.ID == "opus-remote" {
			found = true
		}
	}
	if !found {
		t.Error("refreshed catalog should serve the remote-only model")
	}
}

// TestRefreshRemoteModels_Non200 verifies a non-200 remote leaves the merge
// untouched and surfaces an error.
func TestRefreshRemoteModels_Non200(t *testing.T) {
	isolateHome(t)
	resetModelCatalog()
	defer resetModelCatalog()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // GitHub rate limit shape
	}))
	defer srv.Close()

	oldURL := remoteModelsURL
	remoteModelsURL = srv.URL
	defer func() { remoteModelsURL = oldURL }()

	if err := RefreshRemoteModels(context.Background()); err == nil {
		t.Error("non-200 remote should return an error")
	}
	// Embedded baseline still served.
	if len(SeedModels(TypeClaude)) == 0 {
		t.Error("embedded baseline should still be served after a failed refresh")
	}
}
