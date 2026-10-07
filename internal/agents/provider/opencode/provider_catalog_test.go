package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yogasw/wick/internal/agents/config"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// fakeCatalogServer answers /provider and /provider/auth like opencode
// serve does, checking basic auth and the directory query.
func fakeCatalogServer(t *testing.T, dir string, authOK bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != serverUser || p != "pw" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if got := r.URL.Query().Get("directory"); got != dir {
			t.Errorf("directory = %q, want %q", got, dir)
		}
		switch r.URL.Path {
		case "/provider":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"all": []any{
					map[string]any{"id": "openai", "name": "OpenAI", "env": []string{"OPENAI_API_KEY"}, "models": map[string]any{
						"gpt-5.5": map[string]any{"id": "gpt-5.5", "name": "GPT-5.5"},
						"gpt-4":   map[string]any{"id": "gpt-4", "name": "GPT-4", "status": "deprecated"},
					}},
					map[string]any{"id": "opencode", "name": "OpenCode Zen", "env": []string{}, "models": map[string]any{
						"big-pickle": map[string]any{"name": "Big Pickle"},
					}},
				},
				"default":   map[string]string{"openai": "gpt-5.5"},
				"connected": []string{"opencode", "openai"},
			})
		case "/provider/auth":
			if !authOK {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"openai": []any{map[string]any{"type": "oauth", "label": "ChatGPT Pro/Plus (headless)"}, map[string]any{"type": "api", "label": "API key"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestFetchCatalogMapsProviderRoutes(t *testing.T) {
	dir := t.TempDir()
	srv := fakeCatalogServer(t, dir, true)
	defer srv.Close()
	cat, err := fetchCatalog(context.Background(), catalogClient(&serverHandle{url: srv.URL, password: "pw"}, dir))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.Connected, []string{"opencode", "openai"}) || cat.Default["openai"] != "gpt-5.5" {
		t.Fatalf("connected/default = %v %v", cat.Connected, cat.Default)
	}
	var ids []string
	for _, m := range cat.Models() {
		ids = append(ids, m.ID)
	}
	// Deprecated models are dropped; a model without id takes its map key.
	if !slices.Equal(ids, []string{"opencode/big-pickle", "openai/gpt-5.5"}) {
		t.Fatalf("models = %q", ids)
	}
	if p, ok := cat.Provider("openai"); !ok || p.Name != "OpenAI" || !slices.Equal(p.Env, []string{"OPENAI_API_KEY"}) {
		t.Fatalf("openai = %+v", p)
	}
	if m := cat.Auth["openai"]; len(m) != 2 || m[0].Type != "oauth" || m[0].Label != "ChatGPT Pro/Plus (headless)" {
		t.Fatalf("auth = %+v", cat.Auth)
	}
}

// /provider/auth failing still yields the providers (no Auth: the login
// picker falls back to its static list).
func TestFetchCatalogWithoutAuthRoute(t *testing.T) {
	dir := t.TempDir()
	srv := fakeCatalogServer(t, dir, false)
	defer srv.Close()
	cat, err := fetchCatalog(context.Background(), catalogClient(&serverHandle{url: srv.URL, password: "pw"}, dir))
	if err != nil || len(cat.Providers) != 2 || cat.Auth != nil {
		t.Fatalf("cat = %+v err = %v", cat, err)
	}
}

// No running server: a throwaway serve is booted for the listing and
// killed after; a boot that fails is an error (callers fall back).
func TestFetchInstanceCatalogBootsThrowawayServer(t *testing.T) {
	dir := t.TempDir()
	srv := fakeCatalogServer(t, dir, true)
	defer srv.Close()
	prev := catalogServe
	t.Cleanup(func() { catalogServe = prev })
	killed := false
	catalogServe = func(_ context.Context, spec serverSpec, pw string) (*serverHandle, error) {
		if spec.dir != dir || spec.bin == "" {
			t.Errorf("spec = %+v", spec)
		}
		return &serverHandle{url: srv.URL, password: "pw", kill: func() { killed = true }}, nil
	}
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc-cat", Binary: "/bin/sh", OpencodeConfig: &provider.OpencodeConfig{DataDir: dir}}
	// No server running and no explicit Refresh: nothing starts.
	if _, err := fetchInstanceCatalog(context.Background(), ins); !errors.Is(err, errNoLiveServe) {
		t.Fatalf("render-path fetch: %v", err)
	}
	cat, err := fetchInstanceCatalog(provider.WithHelperSpawn(context.Background()), ins)
	if err != nil || len(cat.Providers) != 2 || !killed {
		t.Fatalf("cat = %+v err = %v killed = %v", cat, err, killed)
	}

	catalogServe = func(context.Context, serverSpec, string) (*serverHandle, error) {
		return nil, errNotListening
	}
	if _, err := fetchInstanceCatalog(provider.WithHelperSpawn(context.Background()), ins); !errors.Is(err, errNotListening) {
		t.Fatalf("unreachable: err = %v", err)
	}
}

// The throwaway catalog server is a full opencode process: it starts
// inside the memory guard (spec.wrap set) when the guard is on.
func TestCatalogServerUsesHelperGuard(t *testing.T) {
	dir := t.TempDir()
	srv := fakeCatalogServer(t, dir, true)
	defer srv.Close()
	prevServe, prevGuard := catalogServe, provider.HelperGuard
	t.Cleanup(func() { catalogServe, provider.HelperGuard = prevServe, prevGuard })
	provider.HelperGuard = func() *provider.MemGuard {
		return &provider.MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 600}
	}
	wrapped := false
	catalogServe = func(_ context.Context, spec serverSpec, _ string) (*serverHandle, error) {
		wrapped = spec.wrap != nil
		return &serverHandle{url: srv.URL, password: "pw", kill: func() {}}, nil
	}
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc-guard", Binary: "/bin/sh", OpencodeConfig: &provider.OpencodeConfig{DataDir: dir}}
	if _, err := fetchInstanceCatalog(provider.WithHelperSpawn(context.Background()), ins); err != nil {
		t.Fatal(err)
	}
	if !wrapped {
		t.Fatal("catalog server started outside the memory guard")
	}
}
