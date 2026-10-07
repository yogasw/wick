package slack

import "testing"

// A follow-up message arriving while a turn is still running supersedes the
// turn but must keep pointing at the same live (streamed) Slack message.
// Dropping liveTS/lastSent makes finalizeReply post a FRESH message and
// abandons the live one mid-word — the "double reply, first one truncated"
// bug.
func TestCarryOverKeepsLiveMessagePointer(t *testing.T) {
	old := &turn{hasStarted: true}
	old.buf.WriteString("partial reply ")
	old.liveTS = "111.222"
	old.lastSent = "partial reply"

	fresh := &turn{}
	fresh.carryOver(old)

	if got := fresh.buf.String(); got != "partial reply " {
		t.Errorf("buf = %q, want %q", got, "partial reply ")
	}
	if !fresh.hasStarted {
		t.Error("hasStarted must carry over")
	}
	if fresh.liveTS != "111.222" {
		t.Errorf("liveTS = %q, want %q — losing it orphans the live message", fresh.liveTS, "111.222")
	}
	if fresh.lastSent != "partial reply" {
		t.Errorf("lastSent = %q, want %q", fresh.lastSent, "partial reply")
	}
}

// With the carried liveTS, the finalize path must EDIT the live message, not
// post a new one — reconcilePlan is the decision point finalizeReply uses.
func TestSupersededTurnFinalizesAsEditNotFreshPost(t *testing.T) {
	old := &turn{}
	old.liveTS = "111.222"
	old.lastSent = "partial reply"

	fresh := &turn{}
	fresh.carryOver(old)

	plan := reconcilePlan("full final reply", fresh.liveTS, fresh.lastSent)
	if plan.postFresh {
		t.Fatal("superseded turn must not post a fresh message while a live message exists")
	}
	if !plan.update {
		t.Fatal("superseded turn must edit the existing live message")
	}
}
