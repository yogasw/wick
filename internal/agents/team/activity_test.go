package team

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

func TestUnread(t *testing.T) {
	now := time.Now()
	before, after := now.Add(-time.Minute), now.Add(time.Minute)
	cases := []struct {
		name   string
		active time.Time
		read   *time.Time
		want   bool
	}{
		{"no activity", time.Time{}, nil, false},
		{"never opened", now, nil, true},
		{"read after activity", now, &after, false},
		{"activity after read", now, &before, true},
		{"same instant", now, &now, false},
	}
	for _, c := range cases {
		if got := Unread(c.active, c.read); got != c.want {
			t.Errorf("%s: Unread = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCurrentAction(t *testing.T) {
	use := func(id, name, in string) store.TurnEvent {
		return store.TurnEvent{Type: "tool_use", ToolUseID: id, ToolName: name, ToolInput: in}
	}
	res := func(id string) store.TurnEvent { return store.TurnEvent{Type: "tool_result", ToolUseID: id} }
	cases := []struct {
		name string
		evs  []store.TurnEvent
		want string
	}{
		{"idle", nil, ""},
		{"thinking only", []store.TurnEvent{{Type: "thinking"}}, ""},
		{"tool running", []store.TurnEvent{{Type: "thinking"}, use("a", "Bash", "{}")}, "Bash"},
		{"tool finished", []store.TurnEvent{use("a", "Bash", "{}"), res("a")}, ""},
		{"second tool running", []store.TurnEvent{use("a", "Read", ""), res("a"), use("b", "Grep", "")}, "Grep"},
		{"writing after tool", []store.TurnEvent{use("a", "Read", ""), res("a"), {Type: "text"}}, ""},
		{"connector op", []store.TurnEvent{use("a", "mcp__wick__wick_execute", `{"tool_id":"conn:9f2c/query_range@acc1"}`)}, "query_range"},
	}
	for _, c := range cases {
		if got := CurrentAction(c.evs); got != c.want {
			t.Errorf("%s: CurrentAction = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestActionLabel(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"Bash", "", "Bash"},
		{"mcp__wick__wick_list", "", "wick_list"},
		{"mcp__support-tools__wick_execute", `{"tool_id":"conn:x/send_message"}`, "send_message"},
		{"mcp__wick__wick_execute", `{"calls":[]}`, "wick_execute"},
		{"mcp__wick__wick_execute", `not json`, "wick_execute"},
	}
	for _, c := range cases {
		if got := ActionLabel(c.name, c.input); got != c.want {
			t.Errorf("ActionLabel(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	long := ActionLabel("abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz", "")
	if r := []rune(long); len(r) != maxActionRunes || r[len(r)-1] != '…' {
		t.Errorf("long label not truncated: %q", long)
	}
}
