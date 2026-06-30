package provider_test

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"

	// Blank-import each subpackage so its init() registers its catalog,
	// mirroring how the agents UI module wires them at process start.
	_ "github.com/yogasw/wick/internal/agents/provider/claude"
	_ "github.com/yogasw/wick/internal/agents/provider/codex"
	_ "github.com/yogasw/wick/internal/agents/provider/gemini"
)

func TestCatalogFor(t *testing.T) {
	for _, ty := range provider.SupportedTypes() {
		cat := provider.CatalogFor(ty)
		if len(cat.Env) == 0 {
			t.Errorf("%s: expected at least one env entry (subpackage init not registered?)", ty)
		}
		all := append(append([]provider.CatalogEntry{}, cat.Env...), cat.Args...)
		for _, e := range all {
			if e.Key == "" {
				t.Errorf("%s: catalog entry with empty key", ty)
			}
			switch e.Kind {
			case provider.CatalogBool, provider.CatalogEnum:
				if len(e.Options) == 0 {
					t.Errorf("%s/%s: kind %q requires options", ty, e.Key, e.Kind)
				}
			case provider.CatalogString, provider.CatalogInt:
				// options optional
			default:
				t.Errorf("%s/%s: unknown kind %q", ty, e.Key, e.Kind)
			}
		}
	}
}

func TestCatalogForUnknownType(t *testing.T) {
	cat := provider.CatalogFor(provider.Type("nope"))
	if len(cat.Env) != 0 || len(cat.Args) != 0 {
		t.Fatalf("unknown type should yield empty catalog, got env=%d args=%d", len(cat.Env), len(cat.Args))
	}
}
