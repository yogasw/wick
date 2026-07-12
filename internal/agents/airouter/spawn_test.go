package airouter

import (
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

type fakeHook struct{}

func (fakeHook) DefaultKey() string { return "def-key" }
func (fakeHook) Slots(t provider.Type) []provider.RouterSlot {
	return []provider.RouterSlot{{Key: "m", Label: "M"}}
}
func (fakeHook) Contribute(t provider.Type, ins provider.Instance, base, key string) (args, env []string, err error) {
	return []string{"--base", base}, []string{"BASE=" + base, "KEY=" + key}, nil
}

func TestSpawnContributionResolvesRouterAndBaseURL(t *testing.T) {
	Register(Descriptor{ID: "testrtr", DisplayName: "Test", PrefPort: 31600, Hook: fakeHook{}})
	Init()
	os.Setenv("WICK_PORT", "9999")
	t.Cleanup(func() { os.Unsetenv("WICK_PORT") })

	// Instance that doesn't route → empty contribution.
	empty, err := provider.RouterSpawnContribution(&provider.Instance{UseAIRouter: false, AIRouterProvider: "testrtr"}, provider.TypeClaude)
	if err != nil {
		t.Fatalf("no-route contribution err: %v", err)
	}
	if len(empty.Args) != 0 || len(empty.Env) != 0 {
		t.Fatalf("non-routing instance should contribute nothing, got %+v", empty)
	}

	// Routing instance → hook invoked with the wick-origin base + default key.
	contrib, err := provider.RouterSpawnContribution(&provider.Instance{UseAIRouter: true, AIRouterProvider: "testrtr"}, provider.TypeClaude)
	if err != nil {
		t.Fatalf("contribution err: %v", err)
	}
	joined := strings.Join(contrib.Env, " ")
	if !strings.Contains(joined, "BASE=http://127.0.0.1:9999/airouter/testrtr/v1") {
		t.Fatalf("base URL not threaded to hook: %v", contrib.Env)
	}
	if !strings.Contains(joined, "KEY=def-key") {
		t.Fatalf("default key not resolved: %v", contrib.Env)
	}
	if len(contrib.Args) == 0 {
		t.Fatalf("hook args dropped: %+v", contrib)
	}

	// Slots resolve through the injected lookup.
	if slots := provider.RouterSlots("testrtr", provider.TypeClaude); len(slots) != 1 || slots[0].Key != "m" {
		t.Fatalf("slots not resolved: %+v", slots)
	}
}

func TestSpawnContributionUnknownRouterErrors(t *testing.T) {
	Init()
	os.Setenv("WICK_PORT", "9999")
	t.Cleanup(func() { os.Unsetenv("WICK_PORT") })
	_, err := provider.RouterSpawnContribution(&provider.Instance{UseAIRouter: true, AIRouterProvider: "does-not-exist"}, provider.TypeClaude)
	if err == nil {
		t.Fatal("expected error for unknown router id")
	}
}
