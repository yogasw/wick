package codex

import (
	"context"
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestEffortArgs(t *testing.T) {
	if got := effortArgs(""); got != nil {
		t.Fatalf("no effort must add nothing, got %q", got)
	}
	got := effortArgs("xhigh")
	if !slices.Equal(got, []string{"-c", `model_reasoning_effort="xhigh"`}) {
		t.Fatalf("got %q", got)
	}
}

func TestCodexEffortResolverRegistered(t *testing.T) {
	caps, registered, err := provider.EffortCapabilitiesFor(context.Background(), provider.Instance{Type: provider.TypeCodex, Name: "acme"}, "any-model")
	if err != nil || !registered {
		t.Fatalf("registered=%v err=%v", registered, err)
	}
	var ids []string
	for _, o := range caps.Options {
		ids = append(ids, o.ID)
	}
	if !slices.Equal(ids, []string{"minimal", "low", "medium", "high", "xhigh"}) {
		t.Fatalf("options %v", ids)
	}
	if caps.Default != "" {
		t.Fatalf("codex default must stay unknown, got %q", caps.Default)
	}
}
