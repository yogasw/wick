package agents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

type fakeGroup struct {
	mu      sync.Mutex
	thread  []store.ConversationTurn
	asked   []string
	replies map[string][]string // handle → replies, in order
	prompts []string
}

func (f *fakeGroup) run(members []teamlink.Peer, override int) *groupRun {
	g := session.AgentGroup{Name: "Ops", DefaultResponder: teamlink.ResponderCaptain, MaxHopsOverride: override}
	n := 0
	return &groupRun{
		group: g, members: members, limit: teamlink.GroupLimit(members, override),
		turn: func(_ context.Context, p teamlink.Peer, prompt string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.asked = append(f.asked, p.Handle)
			f.prompts = append(f.prompts, prompt)
			r := f.replies[p.Handle]
			if len(r) == 0 {
				return "ok from " + p.Handle, nil
			}
			f.replies[p.Handle] = r[1:]
			return r[0], nil
		},
		history: func() []store.ConversationTurn { return f.thread },
		record:  func(t store.ConversationTurn) { f.thread = append(f.thread, t) },
		now: func() time.Time {
			n++
			return time.Unix(int64(n), 0)
		},
	}
}

func groupFixture() []teamlink.Peer {
	return []teamlink.Peer{
		{ID: "a-cap", Handle: "captain", IsCaptain: true},
		{ID: "a-anton", Handle: "anton"},
		{ID: "a-vera", Handle: "vera"},
	}
}

func TestGroupRunMembersAnswerSideBySide(t *testing.T) {
	f := &fakeGroup{replies: map[string][]string{}}
	m := groupFixture()
	f.run(m, 0).play(context.Background(), "g-side", []teamlink.Peer{m[1], m[2]})
	if strings.Join(f.asked, ",") != "anton,vera" {
		t.Fatalf("asked = %v", f.asked)
	}
	if len(f.thread) != 2 || f.thread[0].Speaker.Handle != "anton" || f.thread[1].Speaker.Via != store.ViaGroup || f.thread[0].Role != "assistant" {
		t.Fatalf("thread = %+v", f.thread)
	}
	if !strings.Contains(f.prompts[1], "--- @anton:\nok from anton") || !strings.Contains(f.prompts[1], "you are @vera") {
		t.Fatalf("vera's prompt lacks the thread: %s", f.prompts[1])
	}
}

func TestGroupRunAgentMentionsStopAtTheSmallestCap(t *testing.T) {
	f := &fakeGroup{replies: map[string][]string{
		"captain": {"@anton please look", "@anton again", "@anton more"},
		"anton":   {"@captain your turn", "@captain your turn", "@captain your turn"},
	}}
	m := groupFixture()
	m[1].MaxHops = 3 // smallest member cap wins
	f.run(m, 0).play(context.Background(), "g-cap", []teamlink.Peer{m[0]})
	// captain (human turn) + 3 agent-handed turns, then the cap.
	if strings.Join(f.asked, ",") != "captain,anton,captain,anton" {
		t.Fatalf("asked = %v", f.asked)
	}
	last := f.thread[len(f.thread)-1]
	if last.Kind != store.KindHopLimit || last.Extras["max_turns"] != "3" {
		t.Fatalf("last = %+v", last)
	}
}

func TestGroupRunOverrideOnlyLowers(t *testing.T) {
	f := &fakeGroup{replies: map[string][]string{"captain": {"@anton go"}, "anton": {"@vera go"}}}
	m := groupFixture()
	f.run(m, 1).play(context.Background(), "g-ovr", []teamlink.Peer{m[0]})
	if strings.Join(f.asked, ",") != "captain,anton" {
		t.Fatalf("asked = %v", f.asked)
	}
	if last := f.thread[len(f.thread)-1]; last.Kind != store.KindHopLimit || last.Extras["max_turns"] != "1" {
		t.Fatalf("last = %+v", last)
	}
	if teamlink.GroupLimit(m, 9) != teamlink.DefaultMaxHops {
		t.Fatal("a larger override raised the cap")
	}
}

func TestGroupRunRespectsMentionPolicy(t *testing.T) {
	f := &fakeGroup{replies: map[string][]string{"anton": {"@vera can you check"}}}
	m := groupFixture()
	m[2].MentionFrom = teamlink.MentionCaptain
	f.run(m, 0).play(context.Background(), "g-pol", []teamlink.Peer{m[1]})
	if strings.Join(f.asked, ",") != "anton" {
		t.Fatalf("asked = %v", f.asked)
	}
	if last := f.thread[len(f.thread)-1]; last.Kind != store.KindMentionRefused || last.Extras["to"] != "vera" {
		t.Fatalf("last = %+v", last)
	}
}

func TestApplyGroupWrite(t *testing.T) {
	own := []teamlink.Peer{{ID: "a1", MaxHops: 3}, {ID: "a2"}, {ID: "a3"}}
	g := &session.AgentGroup{}
	name, members, over := " Ops  room ", []string{"a1", "a2"}, 2
	if err := applyGroupWrite(g, teamGroupWriteReq{Name: &name, Members: &members, MaxHopsOverride: &over}, own); err != nil {
		t.Fatal(err)
	}
	if g.Name != "Ops room" || len(g.Members) != 2 || g.MaxHopsOverride != 2 {
		t.Fatalf("g = %+v", g)
	}
	over = 4 // above a1's cap of 3
	if err := applyGroupWrite(g, teamGroupWriteReq{MaxHopsOverride: &over}, own); err == nil {
		t.Fatal("override above the member cap accepted")
	}
	one := []string{"a1"}
	if err := applyGroupWrite(&session.AgentGroup{}, teamGroupWriteReq{Members: &one}, own); err == nil {
		t.Fatal("a one-member group accepted")
	}
	bad := "everyone"
	if err := applyGroupWrite(&session.AgentGroup{Members: members}, teamGroupWriteReq{DefaultResponder: &bad}, own); err == nil {
		t.Fatal("bad responder accepted")
	}
	added, removed := memberDiff([]string{"a1", "a2"}, []string{"a2", "a3"})
	if len(added) != 1 || added[0] != "a3" || len(removed) != 1 || removed[0] != "a1" {
		t.Fatalf("diff = %v / %v", added, removed)
	}
}
