package agents

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/teamlink"
)

func traceEvents(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var root struct {
		TurnID string           `json:"turn_id"`
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("trace does not parse: %v", err)
	}
	if root.TurnID != "turn1" {
		t.Fatalf("turn_id lost: %q", root.TurnID)
	}
	return root.Events
}

func TestMergeTeamTrace_ToolCallGetsTaskWithThread(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	trace := `{"turn_id":"turn1","events":[` +
		`{"event_id":"e0","type":"tool_use","tool_name":"mcp__wick__team_message","tool_use_id":"u1","tool_input":"{\"to\":\"@halodev\"}","at":"2026-10-12T09:00:01Z","display":{"kind":"x"}},` +
		`{"event_id":"e1","type":"tool_result","tool_use_id":"u1","text":"{\"task_id\":\"t1\",\"state\":\"working\"}","end_at":"2026-10-12T09:00:02Z"},` +
		`{"event_id":"e2","type":"tool_use","tool_name":"team_message","tool_use_id":"u2","tool_input":"{\"task_id\":\"t0\",\"message\":\"yes\"}"},` +
		`{"event_id":"e3","type":"tool_result","tool_use_id":"u2","text":"{\"task_id\":\"t0\"}"}]}`
	sent := []teamlink.TaskView{
		{TaskID: "t1", ToHandle: "halodev", ToName: "Halodev", State: "completed", Started: t0, EndedAt: t0.Add(2 * time.Minute)},
		{TaskID: "t0", ToHandle: "qa", State: "working", Started: t0.Add(-time.Hour)},
	}
	thread := func(id string) []teamlink.ThreadEntry {
		if id != "t1" {
			t.Errorf("thread read for %s", id)
		}
		return []teamlink.ThreadEntry{{Role: "user", Text: "check the invite API", At: t0}, {Role: "agent", Handle: "halodev", Text: "no bot option", At: t0.Add(time.Minute)}}
	}
	evs := traceEvents(t, mergeTeamTrace([]byte(trace), sent, thread))
	if len(evs) != 5 {
		t.Fatalf("want 4 stored + 1 team_task, got %d: %v", len(evs), evs)
	}
	if evs[0]["display"] == nil {
		t.Error("a stored event lost a field it was not decoded with")
	}
	last := evs[4]
	if last["type"] != "team_task" || last["tool_use_id"] != "u1" {
		t.Fatalf("team_task not tied to the call: %v", last)
	}
	var tt teamTraceTask
	if err := json.Unmarshal([]byte(last["text"].(string)), &tt); err != nil {
		t.Fatal(err)
	}
	if tt.TaskID != "t1" || tt.Via != "tool" || tt.State != "completed" || len(tt.Entries) != 2 || tt.Entries[1].Text != "no bot option" {
		t.Fatalf("team_task text: %+v", tt)
	}
}

func TestMergeTeamTrace_MentionGetsSyntheticHandoff(t *testing.T) {
	trace := `{"turn_id":"turn1","events":[{"type":"text","text":"@halodev please check","at":"2026-10-12T09:00:00Z","end_at":"2026-10-12T09:00:05Z"}]}`
	in := time.Date(2026, 10, 12, 9, 0, 6, 0, time.UTC) // within the slack
	sent := []teamlink.TaskView{
		{TaskID: "t9", ToHandle: "halodev", Title: "please check", State: "working", Started: in},
		{TaskID: "t8", ToHandle: "qa", State: "working", Started: in.Add(time.Hour)},  // another turn
		{TaskID: "t7", ToHandle: "qa", State: "working", Started: in, Origin: "user"}, // the person's own
	}
	sent = append(sent, teamlink.TaskView{TaskID: "t6", ToHandle: "qa", State: "working", Started: in, RetryOf: "t1"}) // a Retry the person pressed
	evs := traceEvents(t, mergeTeamTrace([]byte(trace), sent, nil))
	if len(evs) != 2 || evs[1]["type"] != "team_task" {
		t.Fatalf("want text + one team_task, got %v", evs)
	}
	var tt teamTraceTask
	_ = json.Unmarshal([]byte(evs[1]["text"].(string)), &tt)
	if tt.Via != "mention" || tt.TaskID != "t9" || tt.ToHandle != "halodev" || evs[1]["tool_use_id"] != nil {
		t.Fatalf("mention task: %+v %v", tt, evs[1])
	}
}

func TestMergeTeamTrace_Unchanged(t *testing.T) {
	trace := []byte(`{"turn_id":"turn1","events":[{"type":"text","text":"hi"}]}`)
	if got := mergeTeamTrace(trace, nil, nil); string(got) != string(trace) {
		t.Error("no tasks: trace must come back as is")
	}
	if got := mergeTeamTrace([]byte("not json"), []teamlink.TaskView{{TaskID: "t"}}, nil); string(got) != "not json" {
		t.Error("unparsable trace must come back as is")
	}
	if got := mergeTeamTrace(trace, []teamlink.TaskView{{TaskID: "t"}}, nil); string(got) != string(trace) {
		t.Error("a turn that handed nothing off must come back as is")
	}
}

func TestTeamTraceToolNamespaces(t *testing.T) {
	for _, n := range []string{"team_message", "mcp__wick__team_message", "wick.team_message"} {
		if !teamTraceTool.MatchString(n) {
			t.Errorf("%s must match", n)
		}
	}
	for _, n := range []string{"team_messages", "wick_team_message"} {
		if teamTraceTool.MatchString(n) {
			t.Errorf("%s must not match", n)
		}
	}
}
