package teamlink

import (
	"context"
	"sync"
	"testing"
)

// holdTurns is fakeTurns whose agents run in "sess-<id>" and whose held
// sessions take no agent's turn (HoldChecker).
type holdTurns struct {
	*fakeTurns
	mu   sync.Mutex
	held map[string]bool
}

func (h *holdTurns) MainSession(_ context.Context, agent Peer) string { return "sess-" + agent.ID }

func (h *holdTurns) Held(_ context.Context, sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.held[sessionID]
}

func (h *holdTurns) set(sessionID string, on bool) {
	h.mu.Lock()
	h.held[sessionID] = on
	h.mu.Unlock()
}

func newHoldHub() (*Hub, *holdTurns, *fakeNotify) {
	h, turns, note := newTestHub(func(Peer, string) string { return "on it" })
	ht := &holdTurns{fakeTurns: turns, held: map[string]bool{}}
	h.Turns = ht
	return h, ht, note
}

// An agent's message to a taken-over session runs no turn: it ends
// rejected with the take-over reason, and both handoff rows record it.
func TestHeldSessionRejectsAgentTask(t *testing.T) {
	h, turns, note := newHoldHub()
	turns.set("sess-a-anton", true)
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "@anton", Text: "verify this"})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "rejected" || res.Reason != ErrSessionHeld.Error() {
		t.Fatalf("result = %+v", res)
	}
	if len(turns.seen) != 0 {
		t.Fatalf("a held session ran a turn: %v", turns.seen)
	}
	_, audits := note.snapshot()
	if len(audits) != 2 || audits[0] != "sess-cap captain->anton rejected" || audits[1] != "sess-a-anton captain->anton rejected" {
		t.Fatalf("audits = %v", audits)
	}
}

// A person's own @mention still runs in a taken-over session.
func TestHeldSessionLetsHumanMentionThrough(t *testing.T) {
	h, turns, _ := newHoldHub()
	turns.set("sess-a-anton", true)
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1", To: "@anton", Text: "check logs", Human: true, Mention: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "completed" || len(turns.seen) != 1 {
		t.Fatalf("result = %+v, turns %v", res, turns.seen)
	}
}

// Take over switched off again: an agent's message runs as usual.
func TestHoldOffRunsAgentTask(t *testing.T) {
	h, turns, _ := newHoldHub()
	turns.set("sess-a-anton", true)
	turns.set("sess-a-anton", false)
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "@anton", Text: "verify this"})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "completed" || res.ReplyText != "on it" || len(turns.seen) != 1 {
		t.Fatalf("result = %+v, turns %v", res, turns.seen)
	}
}
