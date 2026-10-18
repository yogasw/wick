package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

func writeConvTurns(t *testing.T, turns ...store.ConversationTurn) string {
	t.Helper()
	var b strings.Builder
	for _, tr := range turns {
		line, err := json.Marshal(tr)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Only a reply the agent wrote on its own — after a sub-agent's result,
// a schedule or a teammate's reply — and not [silent] is a follow-up.
func TestLastOwnTurn(t *testing.T) {
	at := time.Date(2026, 10, 18, 6, 40, 0, 0, time.UTC)
	task := store.ConversationTurn{Role: "user", Source: sourceTeam, Text: "Message from Yelan (@yelan):\nscan"}
	ack := store.ConversationTurn{Role: "assistant", Text: "[silent]"}
	cases := []struct {
		name  string
		turns []store.ConversationTurn
		want  string
	}{
		{"sub-agent result", []store.ConversationTurn{task, ack,
			{Role: "user", Source: "subagent", Text: "result"},
			{Role: "assistant", Text: "Final report: 12 of 12 done", Timestamp: at}}, "Final report: 12 of 12 done"},
		{"schedule, system line between", []store.ConversationTurn{task, ack,
			{Role: "user", Source: "schedule", Text: "check"},
			{Role: "system", Text: "compacted"},
			{Role: "assistant", Text: "build passed", Timestamp: at}}, "build passed"},
		{"silent checkpoint", []store.ConversationTurn{task, ack,
			{Role: "user", Source: "subagent", Text: "result"},
			{Role: "assistant", Text: "[silent] 3/5 done", Timestamp: at}}, ""},
		{"a person's message", []store.ConversationTurn{task, ack,
			{Role: "user", Source: "ui", Text: "hi"},
			{Role: "assistant", Text: "hello", Timestamp: at}}, ""},
		{"interrupted", []store.ConversationTurn{task, ack,
			{Role: "user", Source: "schedule", Text: "check"},
			{Role: "assistant", Text: "half", Interrupted: true, Timestamp: at}}, ""},
	}
	for _, c := range cases {
		text, got, ok := lastOwnTurn(writeConvTurns(t, c.turns...))
		if text != c.want || ok != (c.want != "") || ok && !got.Equal(at) {
			t.Errorf("%s: got %q %v %v, want %q", c.name, text, got, ok, c.want)
		}
	}
	if _, _, ok := lastOwnTurn(filepath.Join(t.TempDir(), "missing.jsonl")); ok {
		t.Error("a missing file gave a follow-up")
	}
}
