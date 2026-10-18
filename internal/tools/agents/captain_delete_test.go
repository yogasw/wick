package agents

import (
	"context"
	"strings"
	"testing"

	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
)

// agents.delete: the handle repeated, never a Captain, never itself, never
// another owner's agent; disable keeps the agent, delete removes it.
func TestCaptainDeleteAgent(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	ops := CaptainOps{}
	refused := func(name string, in teamagents.DeleteInput, want string) {
		t.Helper()
		if _, err := ops.Delete(ctx, "s-captain", in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
	refused("no confirmation", teamagents.DeleteInput{Agent: "@worker"}, "confirm must repeat")
	refused("wrong confirmation", teamagents.DeleteInput{Agent: "@worker", Confirm: "@captain"}, "confirm must repeat")
	refused("itself", teamagents.DeleteInput{Agent: "@captain", Confirm: "@captain"}, "OTHER agents")
	refused("another owner's", teamagents.DeleteInput{Agent: w.stranger.ID, Confirm: "@other"}, "no agent")
	refused("bad mode", teamagents.DeleteInput{Agent: "@worker", Confirm: "worker", Mode: "archive"}, "delete or disable")
	if _, err := ops.Delete(ctx, "s-worker", teamagents.DeleteInput{Agent: "@captain", Confirm: "@captain"}); err == nil {
		t.Error("a worker deleted the Captain")
	}

	out, err := ops.Delete(ctx, "s-captain", teamagents.DeleteInput{Agent: "@worker", Confirm: "@worker", Mode: "disable"})
	if err != nil || out.(map[string]any)["status"] != "disabled" {
		t.Fatalf("disable = %v, %v", out, err)
	}
	if p, err := globalTeam.Get(ctx, w.worker.ID); err != nil || !p.Disabled {
		t.Fatalf("after disable: %+v %v", p, err)
	}

	out, err = ops.Delete(ctx, "s-captain", teamagents.DeleteInput{Agent: "worker", Confirm: "@worker"})
	if err != nil || out.(map[string]any)["status"] != "deleted" {
		t.Fatalf("delete = %v, %v", out, err)
	}
	if _, err := globalTeam.Get(ctx, w.worker.ID); err == nil {
		t.Fatal("the agent is still there")
	}
	if _, ok := globalMgr.Registry().Project(w.worker.ProjectID); !ok {
		t.Error("without purge_chats the project must be kept")
	}
	if _, err := globalTeam.Get(ctx, w.stranger.ID); err != nil {
		t.Errorf("another owner's agent was touched: %v", err)
	}
}
