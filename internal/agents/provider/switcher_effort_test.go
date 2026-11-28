package provider

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// A level belongs to the provider + model it was picked for: a failover
// runs the target's own effort, a return home restores the home's, and a
// manual switch starts from the model default.
func TestApplySwitchEffort(t *testing.T) {
	isolateConfig(t)
	saveSeed(t, Instance{Type: TypeCodex, Name: "codex"})
	saveSeed(t, Instance{Type: TypeClaude, Name: "claude"})
	layout := config.NewLayout(t.TempDir())
	const id = "s-effort"
	seedSession(t, layout, id, "main", "claude/claude")
	if err := session.SetEffort(layout, id, "main", "xhigh"); err != nil {
		t.Fatal(err)
	}

	target := "minimal"
	if _, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{Failover: true, MsgKey: "k1", Effort: &target}); err != nil {
		t.Fatal(err)
	}
	a := agentState(t, layout, id)
	if a.Effort != "minimal" || a.Failover == nil || a.Failover.HomeEffort != "xhigh" {
		t.Fatalf("on the stand-in: effort=%q home=%+v", a.Effort, a.Failover)
	}
	// A pick on the stand-in is the stand-in's.
	if err := session.SetEffort(layout, id, "main", "high"); err != nil {
		t.Fatal(err)
	}
	if a = agentState(t, layout, id); a.Failover.HomeEffort != "xhigh" {
		t.Fatalf("a stand-in pick moved the home: %+v", a.Failover)
	}

	if _, err := ApplySwitch(layout, id, "main", "claude/claude", SwitchOptions{ReturnHome: true}); err != nil {
		t.Fatal(err)
	}
	if a = agentState(t, layout, id); a.Effort != "xhigh" {
		t.Fatalf("return home must restore the home effort, got %q", a.Effort)
	}

	if _, err := ApplySwitch(layout, id, "main", "codex/codex", SwitchOptions{ModelID: "gpt-x"}); err != nil {
		t.Fatal(err)
	}
	if a = agentState(t, layout, id); a.Effort != "" || a.Failover.HomeEffort != "" {
		t.Fatalf("a manual switch starts from the model default: effort=%q home=%+v", a.Effort, a.Failover)
	}
	picked := "high"
	if _, err := ApplySwitch(layout, id, "main", "claude/claude", SwitchOptions{Effort: &picked}); err != nil {
		t.Fatal(err)
	}
	if a = agentState(t, layout, id); a.Effort != "high" || a.Failover.HomeEffort != "high" {
		t.Fatalf("a switch with an effort makes it the home's: effort=%q home=%+v", a.Effort, a.Failover)
	}
}
