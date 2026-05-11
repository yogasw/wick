package capability

import (
	"sync"
	"testing"
)

func TestRegisterLookupRoundtrip(t *testing.T) {
	reset()
	want := Capability{
		HookSupported:  true,
		InterceptScope: "bash+edit+mcp",
	}
	Register("claude", want)

	got, ok := Lookup("claude")
	if !ok {
		t.Fatal("Lookup(claude) returned ok=false after Register")
	}
	if got != want {
		t.Errorf("Lookup mismatch: got %+v, want %+v", got, want)
	}
}

func TestLookupUnknownReturnsZero(t *testing.T) {
	reset()
	got, ok := Lookup("nonexistent")
	if ok {
		t.Errorf("Lookup(nonexistent) ok=true, want false")
	}
	if got != (Capability{}) {
		t.Errorf("Lookup(nonexistent) = %+v, want zero", got)
	}
}

func TestDuplicateRegisterLastWriteWins(t *testing.T) {
	reset()
	Register("codex", Capability{InterceptScope: "shell-only"})
	Register("codex", Capability{InterceptScope: "shell+edit"})

	got, _ := Lookup("codex")
	if got.InterceptScope != "shell+edit" {
		t.Errorf("expected last-write-wins, got %q", got.InterceptScope)
	}
}

func TestAllReturnsSnapshot(t *testing.T) {
	reset()
	Register("a", Capability{HookSupported: true})
	Register("b", Capability{HookSupported: false})

	snap := All()
	if len(snap) != 2 {
		t.Fatalf("All() len = %d, want 2", len(snap))
	}

	// Mutating the snapshot must not affect the registry.
	snap["a"] = Capability{InterceptScope: "tampered"}
	got, _ := Lookup("a")
	if got.InterceptScope == "tampered" {
		t.Error("snapshot mutation leaked back into registry")
	}
}

// TestConcurrentRegister exercises the mutex under -race. The assertion
// is "no race, no panic" — final state depends on goroutine scheduling.
func TestConcurrentRegister(t *testing.T) {
	reset()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			Register("claude", Capability{HookSupported: true, InterceptScope: "bash+edit+mcp"})
		}()
		go func() {
			defer wg.Done()
			_, _ = Lookup("claude")
		}()
	}
	wg.Wait()

	got, ok := Lookup("claude")
	if !ok {
		t.Fatal("expected claude registered after concurrent writes")
	}
	if !got.HookSupported {
		t.Error("final state lost HookSupported flag")
	}
}
