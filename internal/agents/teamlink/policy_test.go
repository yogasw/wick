package teamlink

import (
	"context"
	"errors"
	"testing"
)

// list snapshots the recorded refusals.
func (n *refusalNotify) list() []Refusal {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Refusal(nil), n.refusals...)
}

// setPeer edits one peer of the test directory in place.
func setPeer(h *Hub, id string, edit func(*Peer)) {
	d := h.Dir.(*fakeDir)
	for i := range d.peers {
		if d.peers[i].ID == id {
			edit(&d.peers[i])
		}
	}
}

func TestMentionPolicyEnforced(t *testing.T) {
	ctx := context.Background()
	send := func(h *Hub, from, to string, human bool) error {
		_, err := h.Send(ctx, SendInput{CallerSession: "s-" + from, CallerAgentID: from, To: to, Text: "hi", Mention: true, Human: human})
		return err
	}
	cases := []struct {
		name    string
		policy  string
		allow   []string
		from    string
		human   bool
		refused bool
	}{
		{"all from anyone", MentionAll, nil, "a-vera", false, false},
		{"unset reads as all", "", nil, "a-vera", false, false},
		{"off refuses an agent", MentionOff, nil, "a-cap", false, true},
		{"off still takes a person", MentionOff, nil, "a-vera", true, false},
		{"captain-only takes the captain", MentionCaptain, nil, "a-cap", false, false},
		{"captain-only refuses another agent", MentionCaptain, nil, "a-vera", false, true},
		{"list takes a listed agent", MentionList, []string{"a-vera"}, "a-vera", false, false},
		{"list refuses an unlisted agent", MentionList, []string{"a-vera"}, "a-cap", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
			note := &refusalNotify{}
			h.Notify = note
			setPeer(h, "a-anton", func(p *Peer) { p.MentionFrom, p.MentionAllow = tc.policy, tc.allow })
			err := send(h, tc.from, "anton", tc.human)
			if tc.refused != errors.Is(err, ErrMentionsOff) {
				t.Fatalf("err = %v, want refused=%v", err, tc.refused)
			}
			if tc.refused && len(note.list()) != 1 {
				t.Fatalf("refusal not recorded: %v", note.list())
			}
		})
	}
}

func TestMaxHopsTakesTheSmallerCap(t *testing.T) {
	ctx := context.Background()
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	note := &refusalNotify{}
	h.Notify = note
	setPeer(h, "a-anton", func(p *Peer) { p.MaxHops = 2 })
	setPeer(h, "a-cap", func(p *Peer) { p.MaxHops = 9 })
	res, err := h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "anton", Text: "1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "anton", Text: "2", ContextID: res.ContextID}); err != nil {
		t.Fatal(err)
	}
	_, err = h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "anton", Text: "3", ContextID: res.ContextID})
	if !errors.Is(err, ErrHopLimit) {
		t.Fatalf("third turn under a cap of 2: %v", err)
	}
	r := note.list()
	if len(r) != 1 || !r[0].HopLimit || r[0].MaxTurns != 2 {
		t.Fatalf("refusal = %+v", r)
	}
	// A context keeps the smallest cap it has seen: vera (default 4)
	// joining does not raise anton's 2.
	if _, err := h.Send(ctx, SendInput{CallerSession: "s-cap", CallerAgentID: "a-cap", To: "vera", Text: "4", ContextID: res.ContextID}); !errors.Is(err, ErrHopLimit) {
		t.Fatalf("cap raised by a new member: %v", err)
	}
}

func TestEffectiveAndMinHops(t *testing.T) {
	if EffectiveHops(0) != DefaultMaxHops || EffectiveHops(99) != MaxHopsCeiling || EffectiveHops(3) != 3 {
		t.Fatal("EffectiveHops")
	}
	if got := MinHops(Peer{MaxHops: 6}, Peer{}, Peer{MaxHops: 8}); got != DefaultMaxHops {
		t.Fatalf("MinHops = %d", got)
	}
	if NormalizeMentionFrom("nonsense") != MentionAll || NormalizeMentionFrom(" OFF ") != MentionOff {
		t.Fatal("NormalizeMentionFrom")
	}
}
