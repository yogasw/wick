package channels

import (
	"strings"
	"testing"
)

func TestIsInterimText(t *testing.T) {
	team := []string{"mcp__wick__team_message"}
	cases := []struct {
		name  string
		tools []string
		text  string
		want  bool
	}{
		{"short status line", team, "Sudah saya tanyakan ke Franky dan Atraxa.", true},
		{"bare tool name", []string{"team_message", "team_message"}, "Asked both.", true},
		{"no tool call", nil, "Asked both.", false},
		{"other tool too", []string{"team_message", "Bash"}, "Asked both.", false},
		{"similar name", []string{"team_messages"}, "Asked both.", false},
		{"empty text", team, "  \n", false},
		{"question line", team, "Asked both.\nWhich env do you want?", false},
		{"code fence", team, "Asked both.\n```\nx\n```", false},
		{"table", team, "| a | b |\n|---|---|\n| 1 | 2 |", false},
		{"pipe line without rule", team, "| just a pipe |", true},
		{"too long", team, strings.Repeat("a", InterimMax+1), false},
		{"at the cap", team, strings.Repeat("a", InterimMax), true},
	}
	for _, tc := range cases {
		if got := IsInterimText(tc.tools, tc.text); got != tc.want {
			t.Errorf("%s: IsInterimText = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIsTeamSend(t *testing.T) {
	if !IsTeamSend("mcp__wick__team_message", `{"to":"@franky","message":"cek"}`) {
		t.Error("a send must count")
	}
	if IsTeamSend("team_message", `{"task_id":"t1","message":"yes"}`) {
		t.Error("an answer to a task is not a send")
	}
	if IsTeamSend("Bash", `{}`) {
		t.Error("another tool is not a send")
	}
}

func withTasksActive(t *testing.T, f TasksActiveFn) {
	t.Helper()
	prev := TasksActive
	t.Cleanup(func() { TasksActive = prev })
	TasksActive = f
}

func TestInterimTurnDrop(t *testing.T) {
	open := map[string]bool{"t1": true}
	withTasksActive(t, func(ids []string) bool {
		for _, id := range ids {
			if open[id] {
				return true
			}
		}
		return false
	})
	text := "Sudah saya tanyakan ke Franky."
	result := `{"type":"text","text":"{\"task_id\":\"t1\",\"state\":\"working\"}"}`

	var it InterimTurn
	it.Tool("mcp__wick__team_message", `{"to":"@franky"}`, "u1", true)
	it.Result("", "u1", result)
	if !it.Holding() || !it.Drop(text) {
		t.Fatal("a task still working must drop the line")
	}

	delete(open, "t1")
	if it.Drop(text) {
		t.Fatal("every task settled: the line posts")
	}

	open["t1"] = true
	off := InterimTurn{}
	off.Tool("team_message", `{"to":"@franky"}`, "u1", false)
	off.Result("", "u1", result)
	if off.Holding() || off.Drop(text) {
		t.Fatal("switch off: no hold")
	}

	mixed := InterimTurn{}
	mixed.Tool("team_message", `{"to":"@franky"}`, "u1", true)
	mixed.Result("", "u1", result)
	mixed.Tool("Bash", `{}`, "u2", true)
	if mixed.Holding() || mixed.Drop(text) {
		t.Fatal("a turn that did other work is never held")
	}

	noTask := InterimTurn{}
	noTask.Tool("team_message", `{"to":"@franky"}`, "u1", true)
	noTask.Result("", "u1", `{"error":"no such teammate"}`)
	if noTask.Drop(text) {
		t.Fatal("no task found: the line posts")
	}

	if it.Drop("Which one do you prefer?") {
		t.Fatal("guard 1: a question posts")
	}
}
