package provider

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func seedIDs(ms []ModelSeed) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// Models keeps connected[] order, skips providers that are not connected,
// and leads each block with opencode's default for that provider.
func TestOpencodeCatalogModels(t *testing.T) {
	cat := &OpencodeCatalog{
		Providers: []OpencodeCatalogProvider{
			{ID: "openai", Models: []ModelSeed{{ID: "openai/gpt-5.4"}, {ID: "openai/gpt-5.5"}}},
			{ID: "opencode", Models: []ModelSeed{{ID: "opencode/big-pickle"}}},
			{ID: "groq", Models: []ModelSeed{{ID: "groq/llama"}}},
		},
		Connected: []string{"openai", "opencode", "openai", "missing"},
		Default:   map[string]string{"openai": "gpt-5.5"},
	}
	want := []string{"openai/gpt-5.5", "openai/gpt-5.4", "opencode/big-pickle"}
	if got := seedIDs(cat.Models()); !slices.Equal(got, want) {
		t.Fatalf("models = %q", got)
	}
	var nilCat *OpencodeCatalog
	if nilCat.Models() != nil {
		t.Fatal("nil catalog has models")
	}
}

func withCatalogFetcher(t *testing.T, f func(context.Context, Instance) (*OpencodeCatalog, error)) {
	t.Helper()
	prev := OpencodeCatalogFetcher
	OpencodeCatalogFetcher = f
	t.Cleanup(func() { OpencodeCatalogFetcher = prev })
}

// The opencode model list comes from the server catalog; the CLI only
// answers when the catalog fetch fails.
func TestListCLIModelsOpencodeCatalogThenCLI(t *testing.T) {
	old := cliModelsRunner
	t.Cleanup(func() { cliModelsRunner = old })
	cliRuns := 0
	cliModelsRunner = func(context.Context, string, []string, []string) ([]byte, error) {
		cliRuns++
		return []byte("openai/from-cli\n"), nil
	}
	ins := Instance{Type: TypeOpencode, Name: "oc", Binary: "/bin/sh", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}

	withCatalogFetcher(t, func(context.Context, Instance) (*OpencodeCatalog, error) {
		return &OpencodeCatalog{
			Providers: []OpencodeCatalogProvider{{ID: "openai", Models: []ModelSeed{{ID: "openai/b"}, {ID: "openai/a"}}}},
			Connected: []string{"openai"},
			Default:   map[string]string{"openai": "b"},
		}, nil
	})
	got, err := ListCLIModels(context.Background(), ins)
	if err != nil || !slices.Equal(seedIDs(got), []string{"openai/b", "openai/a"}) || cliRuns != 0 {
		t.Fatalf("catalog models = %q err=%v cli=%d", seedIDs(got), err, cliRuns)
	}

	withCatalogFetcher(t, func(context.Context, Instance) (*OpencodeCatalog, error) {
		return nil, errors.New("connection refused")
	})
	got, err = ListCLIModels(context.Background(), ins)
	if err != nil || !slices.Equal(seedIDs(got), []string{"openai/from-cli"}) || cliRuns != 1 {
		t.Fatalf("fallback models = %q err=%v cli=%d", seedIDs(got), err, cliRuns)
	}
}

// A failed refresh keeps the last good catalog; Peek serves the cache.
func TestFetchOpencodeCatalogKeepsLastGood(t *testing.T) {
	ins := Instance{Type: TypeOpencode, Name: "oc", OpencodeConfig: &OpencodeConfig{DataDir: t.TempDir()}}
	good := &OpencodeCatalog{Connected: []string{"openai"}}
	withCatalogFetcher(t, func(context.Context, Instance) (*OpencodeCatalog, error) { return good, nil })
	if cat, err := FetchOpencodeCatalog(context.Background(), ins); err != nil || cat != good {
		t.Fatalf("first fetch: %v %v", cat, err)
	}
	withCatalogFetcher(t, func(context.Context, Instance) (*OpencodeCatalog, error) { return nil, errors.New("down") })
	cat, err := FetchOpencodeCatalog(context.Background(), ins)
	if err == nil || cat != good {
		t.Fatalf("failed refresh: %v %v", cat, err)
	}
	if PeekOpencodeCatalog(ins) != good {
		t.Fatal("peek lost the last good catalog")
	}
}
