package teamlink

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// sharedDir is fakeDir plus agents shared with a user (by user id).
type sharedDir struct {
	fakeDir
	mu     sync.Mutex
	shared map[string][]string // user → agent ids
}

func (d *sharedDir) SharedPeers(_ context.Context, user string) ([]Peer, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []Peer
	for _, id := range d.shared[user] {
		for _, p := range d.peers {
			if p.ID == id && p.OwnerID != user {
				p.ChatUser = user
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// chatTurns records whose chat each turn ran in.
type chatTurns struct {
	mu   sync.Mutex
	runs []string
}

func (c *chatTurns) Run(_ context.Context, agent Peer, _ string) (string, string, error) {
	c.mu.Lock()
	c.runs = append(c.runs, agent.Handle+"@"+agent.ChatUser)
	c.mu.Unlock()
	return "sess-" + agent.ID + "-" + agent.ChatUser, "ok", nil
}

func newShareHub() (*Hub, *sharedDir, *chatTurns) {
	dir := &sharedDir{
		fakeDir: fakeDir{peers: []Peer{
			{ID: "b-cap", OwnerID: "bob", Handle: "captain", IsCaptain: true},
			{ID: "b-anton", OwnerID: "bob", Handle: "anton"},
			{ID: "a-anton", OwnerID: "alice", Handle: "anton"},
			{ID: "a-lena", OwnerID: "alice", Handle: "lena"},
			{ID: "a-kim", OwnerID: "alice", Handle: "kim"},
		}},
		shared: map[string][]string{"bob": {"a-lena", "a-anton"}},
	}
	turns := &chatTurns{}
	h := NewHub(dir, turns, &fakeNotify{})
	h.poll = 5 * time.Millisecond
	return h, dir, turns
}

func TestSharedAgentTakesRecipientMention(t *testing.T) {
	h, _, turns := newShareHub()
	ctx := context.Background()
	res, err := h.Send(ctx, SendInput{CallerSession: "s-bob", CallerAgentID: "b-cap", To: "@lena", Text: "hi", Human: true, Mention: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "completed" {
		t.Fatalf("state = %s", res.State)
	}
	// The turn runs in bob's chat with lena, not alice's.
	if len(turns.runs) != 1 || turns.runs[0] != "lena@bob" {
		t.Fatalf("runs = %v", turns.runs)
	}
	// bob's own @anton wins over the shared one of the same handle.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bob", CallerAgentID: "b-cap", To: "@anton", Text: "x", Human: true, Mention: true}); err != nil {
		t.Fatal(err)
	}
	if turns.runs[1] != "anton@" {
		t.Fatalf("own agent should win the handle: %v", turns.runs)
	}
}

func TestSharedAgentRefusals(t *testing.T) {
	h, dir, _ := newShareHub()
	ctx := context.Background()
	// The recipient's own agent may mention an agent shared with its
	// owner; it runs in that owner's chat with it.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bob", CallerAgentID: "b-cap", To: "@lena", Text: "hi", Mention: true}); err != nil {
		t.Fatalf("agent mention = %v, want delivered", err)
	}
	// Not shared → unknown.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bob", CallerAgentID: "b-cap", To: "@kim", Text: "hi", Human: true, Mention: true}); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("unshared = %v, want ErrUnknownHandle", err)
	}
	// Unshared after resolve: the executor re-checks and fails the turn.
	dir.mu.Lock()
	dir.shared["bob"] = nil
	dir.mu.Unlock()
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-bob", CallerAgentID: "b-cap", To: "@lena", Text: "hi", Human: true, Mention: true}); !errors.Is(err, ErrUnknownHandle) {
		t.Fatalf("after unshare = %v, want ErrUnknownHandle", err)
	}
}
