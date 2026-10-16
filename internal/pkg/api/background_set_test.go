package api

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// fakeSets is a backgroundSets over fixed sources, recording dispatches.
func fakeSets(subs map[string][]delegation.Survivor, tasks []teamlink.LiveTask, hidden bool) (*backgroundSets, *[]string, map[string][]agentchannels.DetachedSurvivor) {
	var mu sync.Mutex
	order := []string{}
	got := map[string][]agentchannels.DetachedSurvivor{}
	b := &backgroundSets{
		subAgents: func(_ context.Context, s string, _ bool) ([]delegation.Survivor, error) {
			return subs[s], nil
		},
		liveTasks: func() []teamlink.LiveTask { return tasks },
		progress:  func(string) (int, int) { return 1, 3 },
		hidden:    func(string) bool { return hidden },
		dispatch: func(s string, set []agentchannels.DetachedSurvivor) {
			mu.Lock()
			defer mu.Unlock()
			order = append(order, s)
			got[s] = set
		},
		liveParents: func(context.Context) ([]string, error) {
			out := []string{}
			for p := range subs {
				out = append(out, p)
			}
			return out, nil
		},
	}
	return b, &order, got
}

var sampleTasks = []teamlink.LiveTask{
	{FromSession: "cap", ToHandle: "halodev", Working: true},
	{FromSession: "cap", ToHandle: "qa", Working: true},
	{FromSession: "cap", ToHandle: "halodev", Working: true}, // second task, same teammate
	{FromSession: "cap", ToHandle: "docs", Working: false},   // asking back: not working
	{FromSession: "other", ToHandle: "ops", Working: true},
}

// The set a channel gets: sub-agents, then one entry per working teammate,
// with the batch counts.
func TestBackgroundSetMergesTeammates(t *testing.T) {
	b, _, got := fakeSets(map[string][]delegation.Survivor{"cap": {{Handle: "wick-fixer", ProfileKey: "impl"}}}, sampleTasks, false)
	b.report("cap")
	want := []agentchannels.DetachedSurvivor{
		{Handle: "wick-fixer", ProfileKey: "impl"},
		{Handle: "halodev", Teammate: true, Replied: 1, Of: 3},
		{Handle: "qa", Teammate: true, Replied: 1, Of: 3},
	}
	if !reflect.DeepEqual(got["cap"], want) {
		t.Fatalf("set = %+v\nwant %+v", got["cap"], want)
	}
}

func TestBackgroundSetHiddenTeammates(t *testing.T) {
	b, _, got := fakeSets(nil, sampleTasks, true)
	b.report("cap")
	if len(got["cap"]) != 2 {
		t.Fatalf("set = %+v", got["cap"])
	}
	for _, a := range got["cap"] {
		if !a.Hidden || !a.Teammate {
			t.Fatalf("entry not hidden: %+v", a)
		}
	}
}

// A failed read of the rows keeps what the channels have: no dispatch.
func TestBackgroundSetReadErrorKeepsSet(t *testing.T) {
	b, order, _ := fakeSets(nil, sampleTasks, false)
	b.subAgents = func(context.Context, string, bool) ([]delegation.Survivor, error) { return nil, errors.New("db down") }
	b.report("cap")
	if len(*order) != 0 {
		t.Fatalf("dispatched on a read error: %v", *order)
	}
	if _, ok := b.recheck()(context.Background(), "cap"); ok {
		t.Fatal("recheck ok on a read error")
	}
}

// Nothing in the background: an empty set clears the banner.
func TestBackgroundSetEmpty(t *testing.T) {
	b, order, got := fakeSets(nil, nil, false)
	b.report("cap")
	if len(*order) != 1 || len(got["cap"]) != 0 {
		t.Fatalf("order %v set %+v", *order, got["cap"])
	}
}

// After a restart every waiting session is re-reported once: teammates'
// senders and sub-agents' parents.
func TestBackgroundSetReplayAll(t *testing.T) {
	b, order, got := fakeSets(map[string][]delegation.Survivor{"lead": {{Handle: "fixer"}}, "cap": {{Handle: "x"}}}, sampleTasks, false)
	if n := b.replayAll(context.Background()); n != 3 {
		t.Fatalf("replayed %d sessions, want 3", n)
	}
	if want := []string{"cap", "lead", "other"}; !reflect.DeepEqual(*order, want) {
		t.Fatalf("replay order %v, want %v", *order, want)
	}
	if len(got["other"]) != 1 || got["other"][0].Handle != "ops" {
		t.Fatalf("other = %+v", got["other"])
	}
}

// The OnTeamTaskMoved wiring: a just-sent working task pings once (marked
// hidden when the agent hides teammates), a later move only re-reports.
func TestTeamTaskMovedStartPing(t *testing.T) {
	for _, hide := range []bool{false, true} {
		b, order, _ := fakeSets(nil, sampleTasks, hide)
		type ping struct {
			session string
			mate    agentchannels.DetachedSurvivor
			task    string
		}
		var pings []ping
		f := teamTaskMoved(b, func(string) bool { return hide }, func(s string, m agentchannels.DetachedSurvivor, task string) {
			pings = append(pings, ping{s, m, task})
		})
		long := strings.Repeat("x", 100)
		f("cap", teamlink.TaskView{ToHandle: "halodev", State: "working", Title: long}, true)
		f("cap", teamlink.TaskView{ToHandle: "halodev", State: "working"}, false)
		f("cap", teamlink.TaskView{ToHandle: "halodev", State: "completed"}, false)
		f("cap", teamlink.TaskView{ToHandle: "qa", State: "input_required"}, true)
		if len(pings) != 1 {
			t.Fatalf("hide=%v: %d pings, want 1: %+v", hide, len(pings), pings)
		}
		p := pings[0]
		if p.session != "cap" || p.mate != (agentchannels.DetachedSurvivor{Handle: "halodev", Teammate: true, Hidden: hide}) || p.task != strings.Repeat("x", 80)+"…" {
			t.Fatalf("hide=%v: ping %+v", hide, p)
		}
		if len(*order) != 4 {
			t.Fatalf("hide=%v: reported %d times, want every move (4)", hide, len(*order))
		}
	}
}

// S3: after the setting changed, every session the agent waits in is
// re-reported once with the new Hidden.
func TestReportAgentAfterVisibilityChange(t *testing.T) {
	tasks := []teamlink.LiveTask{
		{FromSession: "s1", FromAgentID: "a1", ToHandle: "halodev", Working: true},
		{FromSession: "s1", FromAgentID: "a1", ToHandle: "qa", Working: true},
		{FromSession: "s2", FromAgentID: "a1", ToHandle: "ops", Working: true},
		{FromSession: "s3", FromAgentID: "a2", ToHandle: "ops", Working: true},
	}
	b, order, got := fakeSets(nil, tasks, false)
	b.hidden = func(string) bool { return true } // just switched off
	b.reportAgent("a1")
	if !reflect.DeepEqual(*order, []string{"s1", "s2"}) {
		t.Fatalf("reported %v, want s1 s2", *order)
	}
	for _, a := range append(got["s1"], got["s2"]...) {
		if !a.Hidden {
			t.Fatalf("entry not hidden after the change: %+v", a)
		}
	}
}
