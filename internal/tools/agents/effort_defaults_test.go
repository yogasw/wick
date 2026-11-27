package agents

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
)

func TestApplyProjectFieldsEffortFollowsPair(t *testing.T) {
	s := func(v string) *string { return &v }
	m := project.Meta{Defaults: project.Defaults{Provider: "claude/claude", Model: "m1"}}
	if !applyProjectFields(&m, teamAgentWriteReq{Effort: s("high")}) || m.Defaults.Effort != "high" {
		t.Fatalf("effort not set: %+v", m.Defaults)
	}
	if applyProjectFields(&m, teamAgentWriteReq{Name: s("x")}); m.Defaults.Effort != "high" {
		t.Fatalf("an unrelated write must keep the effort: %+v", m.Defaults)
	}
	applyProjectFields(&m, teamAgentWriteReq{Model: s("m2")})
	if m.Defaults.Effort != "" {
		t.Fatalf("a new model without an effort must drop it: %+v", m.Defaults)
	}
	applyProjectFields(&m, teamAgentWriteReq{Provider: s("codex/codex"), Effort: s("xhigh")})
	if m.Defaults.Effort != "xhigh" {
		t.Fatalf("a new pair with its own effort keeps it: %+v", m.Defaults)
	}
	applyProjectFields(&m, teamAgentWriteReq{Effort: s("default")})
	if m.Defaults.Effort != "" {
		t.Fatalf("default is stored as empty: %+v", m.Defaults)
	}
}

func TestNextDefaultEffort(t *testing.T) {
	ctx := context.Background()
	s := func(v string) *string { return &v }
	d := project.Defaults{Provider: "claude/claude", Model: "m1", Effort: "high"}
	pair := d.Provider + "\x00" + d.Model
	if v, err := nextDefaultEffort(ctx, d, pair, nil); err != nil || v != "high" {
		t.Fatalf("same pair keeps: %q %v", v, err)
	}
	if v, _ := nextDefaultEffort(ctx, d, "codex/codex\x00", nil); v != "" {
		t.Fatalf("changed pair drops: %q", v)
	}
	for _, w := range []string{"", "default", " default "} {
		if v, err := nextDefaultEffort(ctx, d, pair, s(w)); err != nil || v != "" {
			t.Fatalf("%q must clear: %q %v", w, v, err)
		}
	}
	if _, err := nextDefaultEffort(ctx, project.Defaults{}, "", s("high")); err == nil {
		t.Fatal("an effort without a provider must be refused")
	}
}

func TestSameDefaultPair(t *testing.T) {
	d := project.Defaults{Provider: "claude", Model: ""}
	if !sameDefaultPair(d, "claude/claude", "") {
		t.Fatal("bare type and no pin must match the instance default")
	}
	if sameDefaultPair(d, "claude/claude", "m9") {
		t.Fatal("a hand-picked model is not the default pair")
	}
	if sameDefaultPair(d, "codex/codex", "") {
		t.Fatal("another provider is not the default pair")
	}
}
