package teamlink

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// locTurns is fanTurns whose turns run in a session known up front, as
// they do in wick (a paired chat or the main one).
type locTurns struct{ *fanTurns }

func (locTurns) MainSession(_ context.Context, agent Peer) string { return "sess-" + agent.ID }

// The agent answering a task does not also message the asker on the side:
// its final answer is the reply. separate=true lets one through, and a
// final answer that only repeats it is not delivered a second time.
func TestReplyInTaskGuard(t *testing.T) {
	h, turns, note := newFanHub(t, false)
	h.Turns = locTurns{turns}
	a := fanSend(t, h, "anton", nil)
	waitFor(t, "anton's turn running", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.answeringForLocked("a-anton", "sess-a-anton", "a-cap") != ""
	})
	ctx := context.Background()
	side := SendInput{CallerSession: "sess-a-anton", CallerAgentID: "a-anton", To: "captain", Text: "done: 12 of 12", Wait: -1}
	if _, err := h.Send(ctx, side); !errors.Is(err, ErrReplyInTask) || !strings.Contains(err.Error(), a.TaskID) {
		t.Fatalf("side message to the asker = %v, want ErrReplyInTask naming %s", err, a.TaskID)
	}
	// Another chat of the same agent is not answering this task.
	other := side
	other.CallerSession = "sess-a-anton-2"
	if _, err := h.Send(ctx, other); errors.Is(err, ErrReplyInTask) {
		t.Fatal("a message from another chat was taken for the reply")
	}
	turns.line("captain") <- "[silent]"
	// To someone else than the asker: not a reply.
	vera := side
	vera.To = "vera"
	if _, err := h.Send(ctx, vera); errors.Is(err, ErrReplyInTask) {
		t.Fatal("a message to another teammate was refused")
	}
	turns.line("vera") <- "[silent]"
	side.Separate = true
	if _, err := h.Send(ctx, side); err != nil {
		t.Fatalf("separate message refused: %v", err)
	}
	turns.line("captain") <- "[silent]"
	turns.line("anton") <- "Done: 12 of 12."
	settled(t, h, a.TaskID, "completed")
	for _, d := range quiet(t, note, 0) {
		t.Errorf("delivered again: %q", d)
	}
}

// A final answer that says more than the side message is still the reply.
func TestReplyInTaskNewTextDelivered(t *testing.T) {
	h, turns, note := newFanHub(t, false)
	h.Turns = locTurns{turns}
	a := fanSend(t, h, "anton", nil)
	waitFor(t, "anton's turn running", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.answeringForLocked("a-anton", "sess-a-anton", "a-cap") != ""
	})
	side := SendInput{CallerSession: "sess-a-anton", CallerAgentID: "a-anton", To: "captain", Text: "half way", Wait: -1, Separate: true}
	if _, err := h.Send(context.Background(), side); err != nil {
		t.Fatal(err)
	}
	turns.line("captain") <- "[silent]"
	turns.line("anton") <- "all 12 done"
	settled(t, h, a.TaskID, "completed")
	got := quiet(t, note, 1)
	if !strings.Contains(got[0], "all 12 done") {
		t.Fatalf("delivered = %q", got)
	}
}
