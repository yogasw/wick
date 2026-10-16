package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// withProvider registers p under t for one test and restores what was
// there before.
func withProvider(t *testing.T, typ provider.Type, p Provider) {
	t.Helper()
	old, had := lookup(typ)
	Register(typ, p)
	t.Cleanup(func() {
		if had {
			Register(typ, old)
		} else {
			Register(typ, Provider{})
		}
	})
}

func TestRegistryRegisterLookupSupported(t *testing.T) {
	const typ = provider.Type("fake-term")
	ins := provider.Instance{Type: typ, Name: "x"}
	if Supported(ins) || Commands(ins) != nil {
		t.Fatal("an unregistered type has no web terminal")
	}
	if _, ok := LookupCommand(ins, "tui"); ok {
		t.Fatal("an unregistered type has no commands")
	}
	if err := Prepare(ins); err != nil {
		t.Fatalf("Prepare of an unregistered type = %v, want nil", err)
	}

	prepErr := errors.New("prep")
	var prepared provider.Instance
	withProvider(t, typ, Provider{
		Commands: func(provider.Instance) []Command { return []Command{{Key: "tui", Label: "fake"}} },
		Env:      func(_ provider.Instance, env []string) []string { return append(env, "FAKE=1", "GOTTY_X=1") },
		Prepare:  func(i provider.Instance) error { prepared = i; return prepErr },
	})
	if !Supported(ins) {
		t.Fatal("registered type must be supported")
	}
	if c, ok := LookupCommand(ins, "tui"); !ok || c.Label != "fake" {
		t.Fatalf("tui = %+v", c)
	}
	env := Env(ins)
	if !slices.Contains(env, "FAKE=1") || slices.Contains(env, "GOTTY_X=1") {
		t.Errorf("Env must apply the type's adjustment and still drop GOTTY_*: %v", env)
	}
	if err := Prepare(ins); !errors.Is(err, prepErr) || prepared.Name != "x" {
		t.Errorf("Prepare = %v (instance %+v)", err, prepared)
	}

	// An empty allowlist is not a terminal.
	Register(typ, Provider{Commands: func(provider.Instance) []Command { return nil }})
	if Supported(ins) {
		t.Error("empty allowlist must not be supported")
	}
	// A nil Commands unregisters.
	Register(typ, Provider{})
	if _, ok := lookup(typ); ok {
		t.Error("nil Commands must unregister the type")
	}
}

func TestRegistryBuiltinTypes(t *testing.T) {
	for _, typ := range []provider.Type{provider.TypeOMP, provider.TypeOpencode, provider.TypeClaude, provider.TypeCodex, provider.TypeGemini} {
		if !Supported(provider.Instance{Type: typ, Name: "x"}) {
			t.Errorf("%s must have a web terminal", typ)
		}
	}
	if Supported(provider.Instance{Type: provider.TypeWick, Name: "wick"}) {
		t.Error("wick has no web terminal")
	}
}

// Opencode's Prepare creates the data dir its env points at, and its Env
// blanks the config overrides.
func TestOpencodePrepareAndEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "x", OpencodeConfig: &provider.OpencodeConfig{DataDir: dir}}
	if err := Prepare(ins); err != nil {
		t.Fatalf("Prepare = %v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("Prepare must create %s: %v", dir, err)
	}
	env := Env(ins)
	for _, kv := range []string{"OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR="} {
		if !slices.Contains(env, kv) {
			t.Errorf("opencode env must carry %q", kv)
		}
	}
}
