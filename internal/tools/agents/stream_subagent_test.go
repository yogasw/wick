package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// parentMap is an injectable parentOf: ids absent from the map are
// unknown sessions.
func parentMap(m map[string]string) func(string) (string, bool) {
	return func(id string) (string, bool) {
		p, ok := m[id]
		return p, ok
	}
}

// drain collects whatever is buffered on ch right now.
func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		case <-time.After(20 * time.Millisecond):
			return out
		}
	}
}

func TestSubAgentSignalReachesOnlyTheDirectParent(t *testing.T) {
	b := NewBroadcaster()
	b.parentOf = parentMap(map[string]string{"LEAD": "", "CHILD": "LEAD", "OTHER": ""})
	lead, unsubLead := b.Subscribe("LEAD")
	defer unsubLead()
	other, unsubOther := b.Subscribe("OTHER")
	defer unsubOther()
	global, unsubGlobal := b.Subscribe("")
	defer unsubGlobal()

	b.PublishLifecycle(context.Background(), "CHILD", "agent", "working", "claude", 1)
	b.Publish("CHILD", "agent", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash", ToolInput: `{"command":"secret"}`})
	b.Publish("CHILD", "agent", event.AgentEvent{Type: event.Done})

	got := drain(lead)
	if len(got) != 2 {
		t.Fatalf("leader got %d events, want 2 sub_agent signals (lifecycle + turn): %+v", len(got), got)
	}
	want := []string{"working", "turn"}
	for i, ev := range got {
		if ev.Type != evSubAgent || ev.SessionID != "LEAD" {
			t.Fatalf("event %d = %+v, want a sub_agent signal addressed to LEAD", i, ev)
		}
		var d subAgentSignalData
		if err := json.Unmarshal([]byte(ev.Data), &d); err != nil {
			t.Fatal(err)
		}
		if d.ChildSessionID != "CHILD" || d.State != want[i] {
			t.Fatalf("signal %d = %+v, want CHILD/%s", i, d, want[i])
		}
		// Only the projection: the child's tool and its input stay off
		// the leader's stream.
		if strings.Contains(ev.JSON(), "secret") || ev.ToolName != "" || ev.PID != 0 {
			t.Fatalf("signal carries more than {child, state}: %s", ev.JSON())
		}
	}
	if o := drain(other); len(o) != 0 {
		t.Fatalf("an unrelated session got the child's signal: %+v", o)
	}
	for _, ev := range drain(global) {
		if ev.Type == evSubAgent {
			t.Fatalf("sub_agent must not reach global subscribers: %+v", ev)
		}
	}
}

func TestSubAgentSignalSkipsTopLevelAndUnknownSessions(t *testing.T) {
	b := NewBroadcaster()
	b.parentOf = parentMap(map[string]string{"LEAD": ""})
	for _, id := range []string{"LEAD", "GHOST"} {
		if _, _, ok := b.subAgentSignal(id, Event{Type: "lifecycle", Lifecycle: "working"}); ok {
			t.Fatalf("%s has no parent; no signal expected", id)
		}
	}
}

// End to end through /stream/multi: a non-admin who owns only MINE gets
// the signal for MINE's child and never one for another user's child,
// even when they ask for that user's session too.
func TestStreamMultiSubAgentSignalDoesNotLeak(t *testing.T) {
	withMultiStreamWorld(t, map[string]string{"MINE": "u1", "THEIRS": "u2"})
	globalBcast.parentOf = parentMap(map[string]string{
		"MINE": "", "THEIRS": "", "MYCHILD": "MINE", "THEIRCHILD": "THEIRS",
	})

	_, body := runMultiStream(t, &entity.User{ID: "u1"}, "/stream/multi?sessions=MINE,THEIRS",
		func() {
			globalBcast.PublishLifecycle(context.Background(), "MYCHILD", "agent", "working", "claude", 1)
			globalBcast.PublishLifecycle(context.Background(), "THEIRCHILD", "agent", "working", "claude", 2)
		})

	if !strings.Contains(body, `\"child_session_id\":\"MYCHILD\"`) {
		t.Fatalf("own sub-agent's signal missing; body:\n%s", body)
	}
	if strings.Contains(body, "THEIRCHILD") {
		t.Fatalf("another user's sub-agent leaked; body:\n%s", body)
	}
}
