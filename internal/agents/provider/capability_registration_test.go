package provider_test

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/capability"

	// Blank-import each provider sub-package to trigger init().
	// Mirrors what cmd/wick and cmd/gate do at process start.
	_ "github.com/yogasw/wick/internal/agents/provider/claude"
	_ "github.com/yogasw/wick/internal/agents/provider/codex"
	_ "github.com/yogasw/wick/internal/agents/provider/gemini"
	_ "github.com/yogasw/wick/internal/agents/provider/wick"
)

// TestProviderRegistrationsLoadAll asserts that importing each
// provider sub-package populates the capability registries — proves
// the init() side effects survive optimization and that the registry
// contract matches the implementations.
func TestProviderRegistrationsLoadAll(t *testing.T) {
	cases := []struct {
		name       string
		wantScope  string
	}{
		{"claude", "bash+edit+mcp"},
		{"codex", "shell-only"},
		{"gemini", "untested"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap, ok := capability.Lookup(tc.name)
			if !ok {
				t.Fatalf("capability.Lookup(%s) not registered", tc.name)
			}
			if !cap.HookSupported {
				t.Errorf("%s: HookSupported should be true", tc.name)
			}
			if cap.InterceptScope != tc.wantScope {
				t.Errorf("%s: InterceptScope = %q, want %q", tc.name, cap.InterceptScope, tc.wantScope)
			}
			if _, ok := capability.LookupHookConfigWriter(tc.name); !ok {
				t.Errorf("%s: HookConfigWriter not registered", tc.name)
			}
			if _, ok := capability.LookupProber(tc.name); !ok {
				t.Errorf("%s: Prober not registered", tc.name)
			}
		})
	}

	// wick is the in-process provider: registered so the UI can render
	// its badge, but with the inverse contract — no subprocess hook, no
	// writer, no prober. The gate is enforced natively in the runner.
	t.Run("wick", func(t *testing.T) {
		cap, ok := capability.Lookup("wick")
		if !ok {
			t.Fatal("capability.Lookup(wick) not registered")
		}
		if cap.HookSupported {
			t.Error("wick: HookSupported should be false (in-process, no subprocess hook)")
		}
		if cap.InterceptScope != "in-process" {
			t.Errorf("wick: InterceptScope = %q, want %q", cap.InterceptScope, "in-process")
		}
		if _, ok := capability.LookupHookConfigWriter("wick"); ok {
			t.Error("wick: HookConfigWriter should NOT be registered")
		}
		if _, ok := capability.LookupProber("wick"); ok {
			t.Error("wick: Prober should NOT be registered")
		}
	})
}
