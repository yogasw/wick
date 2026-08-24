package connectors

import (
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// withCleanRegistry isolates a test from the package-level registry, which is
// process-wide and shared with every other test in this package.
func withCleanRegistry(t *testing.T) {
	t.Helper()
	savedExtra, savedListeners, savedUnreg := extra, listeners, unregisterListeners
	extra, listeners, unregisterListeners = nil, nil, nil
	t.Cleanup(func() {
		extra, listeners, unregisterListeners = savedExtra, savedListeners, savedUnreg
	})
}

func keysInRegistry() []string {
	out := make([]string, 0, len(extra))
	for _, m := range extra {
		out = append(out, m.Meta.Key)
	}
	return out
}

// TestUnregister_RemovesTheModule pins the fix for the ghost connector card.
//
// A custom connector's definition can be deleted at runtime, but Register was
// the only way into the registry — so the module stayed for the life of the
// process. The detail page kept rendering from it (name, description,
// operation count) after every row was gone, and because the UI decides
// "custom vs built-in" from custom.Service.DefIDForKey — whose map entry HAD
// been dropped — the leftover presented itself as a built-in. Built-ins have
// no Delete action, so the card became unremovable until a restart.
func TestUnregister_RemovesTheModule(t *testing.T) {
	withCleanRegistry(t)

	Register(connector.Module{Meta: connector.Meta{Key: "ghost"}})
	Register(connector.Module{Meta: connector.Meta{Key: "keeper"}})

	Unregister("ghost")

	got := keysInRegistry()
	if len(got) != 1 || got[0] != "keeper" {
		t.Fatalf("registry = %v, want only [keeper]", got)
	}
}

// TestUnregister_UnknownKeyIsNoop: Delete runs Unregister unconditionally, so
// a key that was never registered (or was already removed) must not panic or
// disturb its neighbours.
func TestUnregister_UnknownKeyIsNoop(t *testing.T) {
	withCleanRegistry(t)

	Register(connector.Module{Meta: connector.Meta{Key: "a"}})
	Unregister("never-registered")
	Unregister("a")
	Unregister("a") // second time: already gone

	if got := keysInRegistry(); len(got) != 0 {
		t.Fatalf("registry = %v, want empty", got)
	}
}

// TestUnregister_NotifiesListeners: the workflow registry mirrors this one, so
// it has to learn about a removal or it keeps offering a node type backed by a
// module nobody can execute.
func TestUnregister_NotifiesListeners(t *testing.T) {
	withCleanRegistry(t)

	var removed []string
	OnUnregister(func(key string) { removed = append(removed, key) })

	Register(connector.Module{Meta: connector.Meta{Key: "mirrored"}})
	Unregister("mirrored")

	if len(removed) != 1 || removed[0] != "mirrored" {
		t.Fatalf("unregister listener saw %v, want [mirrored]", removed)
	}

	// A no-op removal must not fire: a listener that trusts the callback would
	// otherwise drop a module that is still registered elsewhere.
	Unregister("mirrored")
	if len(removed) != 1 {
		t.Fatalf("listener fired %d times; a no-op removal must stay silent", len(removed))
	}
}

// TestUnregister_ThenRegisterAgain: deleting a definition must free its key.
// Re-creating a connector with the same key is a normal recovery path, and the
// boot-time guard rejects a key an existing module already holds.
func TestUnregister_ThenRegisterAgain(t *testing.T) {
	withCleanRegistry(t)

	Register(connector.Module{Meta: connector.Meta{Key: "reused", Name: "first"}})
	Unregister("reused")
	Register(connector.Module{Meta: connector.Meta{Key: "reused", Name: "second"}})

	if len(extra) != 1 {
		t.Fatalf("registry has %d modules, want 1 (no duplicate entry)", len(extra))
	}
	if extra[0].Meta.Name != "second" {
		t.Errorf("module name = %q, want the re-registered %q", extra[0].Meta.Name, "second")
	}
}
