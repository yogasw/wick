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
		{"codex namespace", []string{"wick.team_message"}, "Asked both.", true},
		{"neutral tools beside it", []string{"ToolSearch", "mcp__wick__todo", "TodoWrite", "wick.wick_set_title", "set_title", "team_message"}, "Asked both.", true},
		{"neutral tools only", []string{"ToolSearch", "todo"}, "Asked both.", false},
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

func TestIsTeamMessageTool(t *testing.T) {
	for _, n := range []string{"team_message", "mcp__wick__team_message", "wick.team_message"} {
		if !IsTeamMessageTool(n) {
			t.Errorf("%s must match", n)
		}
	}
	for _, n := range []string{"team_messages", "xteam_message", "wick_team_message", ""} {
		if IsTeamMessageTool(n) {
			t.Errorf("%s must not match", n)
		}
	}
}

func TestIsNeutralTool(t *testing.T) {
	for _, n := range []string{"ToolSearch", "todo", "mcp__wick__todo", "TodoWrite", "wick_set_title", "mcp__wick__wick_set_title", "set_title", "wick.set_title"} {
		if !IsNeutralTool(n) {
			t.Errorf("%s must be neutral", n)
		}
	}
	for _, n := range []string{"Bash", "todos", "Read", "team_message"} {
		if IsNeutralTool(n) {
			t.Errorf("%s must not be neutral", n)
		}
	}
}

func TestInterimTurnFinal(t *testing.T) {
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
	dropped := func(it *InterimTurn, text string) bool {
		_, d := it.Final(text)
		return d
	}

	// The line written before the call; the result lands after it.
	var it InterimTurn
	it.Tool("mcp__wick__team_message", `{"to":"@franky"}`, "u1", true)
	it.Result("", "u1", result)
	if post, d := it.Final(text); !it.Holding() || !d || post != "" {
		t.Fatalf("a task still working must drop the line: %q %v", post, d)
	}

	delete(open, "t1")
	if dropped(&it, text) {
		t.Fatal("every task settled: the line posts")
	}

	open["t1"] = true
	off := InterimTurn{}
	off.Tool("team_message", `{"to":"@franky"}`, "u1", false)
	off.Result("", "u1", result)
	if off.Holding() || dropped(&off, text) {
		t.Fatal("switch off: no hold")
	}

	mixed := InterimTurn{}
	mixed.Tool("team_message", `{"to":"@franky"}`, "u1", true)
	mixed.Result("", "u1", result)
	mixed.Tool("Bash", `{}`, "u2", true)
	if mixed.Holding() || dropped(&mixed, text) {
		t.Fatal("a turn that did other work is never held")
	}

	neutral := InterimTurn{}
	neutral.Tool("ToolSearch", `{}`, "n1", true)
	neutral.Tool("team_message", `{"to":"@franky"}`, "u1", true)
	neutral.Result("", "u1", result)
	neutral.Tool("mcp__wick__todo", `{}`, "n2", true)
	neutral.Tool("TodoWrite", `{}`, "n3", true)
	neutral.Tool("mcp__wick__wick_set_title", `{}`, "n4", true)
	if !neutral.Holding() || !dropped(&neutral, text) {
		t.Fatal("neutral tools must not cancel the hold")
	}

	codex := InterimTurn{}
	codex.Tool("wick.team_message", `{"to":"@franky"}`, "u1", true)
	codex.Result("wick.team_message", "u1", result)
	if !codex.Holding() || !dropped(&codex, text) {
		t.Fatal("wick.team_message must hold like team_message")
	}

	noTask := InterimTurn{}
	noTask.Tool("team_message", `{"to":"@franky"}`, "u1", true)
	noTask.Result("", "u1", `{"error":"no such teammate"}`)
	if dropped(&noTask, text) {
		t.Fatal("no task found: the line posts")
	}

	q := "Which one do you prefer?"
	if dropped(&it, q) {
		t.Fatal("guard 1: a question posts")
	}
}
