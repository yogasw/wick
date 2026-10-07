package team

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

var t0 = time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func handoff(s int, task, state string) store.ConversationTurn {
	return store.ConversationTurn{Timestamp: at(s), Role: "system", Kind: store.KindMentionHandoff,
		Extras: map[string]string{"from": "captain", "to": "helper", "task_id": task, "state": state}}
}

func writeConv(t *testing.T, turns ...store.ConversationTurn) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(`{"_meta":{"version":1}}` + "\n")
	for _, tr := range turns {
		b, _ := json.Marshal(tr)
		f.Write(append(b, '\n'))
	}
	return path
}

func TestUnreadCount(t *testing.T) {
	person := store.ConversationTurn{Timestamp: at(0), Role: "user", Source: "ui", Text: "hi"}
	reply := func(s int) store.ConversationTurn {
		return store.ConversationTurn{Timestamp: at(s), Role: "assistant", Text: "answer"}
	}
	asked := func(s int) store.ConversationTurn {
		return store.ConversationTurn{Timestamp: at(s), Role: "user", Source: SourceTeam, Text: "question"}
	}
	before := at(-10)
	for name, tc := range map[string]struct {
		turns   []store.ConversationTurn
		read    *time.Time
		now     time.Time
		want    int
		settled bool
	}{
		"person asked":         {[]store.ConversationTurn{person, reply(1)}, &before, at(5), 1, true},
		"silent reply":         {[]store.ConversationTurn{person, reply(1), {Timestamp: at(2), Role: "assistant", Text: "  [SILENT] run 3/5: ok"}}, &before, at(5), 1, true},
		"read already":         {[]store.ConversationTurn{person, reply(1)}, ptr(at(2)), at(5), 0, true},
		"never opened":         {[]store.ConversationTurn{person, reply(1), person, reply(3)}, nil, at(5), 2, true},
		"teammate, delivered":  {[]store.ConversationTurn{handoff(0, "t1", "TASK_STATE_WORKING"), asked(0), reply(1), handoff(1, "t1", "TASK_STATE_COMPLETED")}, &before, at(5), 0, true},
		"teammate, failed":     {[]store.ConversationTurn{handoff(0, "t1", "TASK_STATE_WORKING"), asked(0), reply(1), handoff(1, "t1", "TASK_STATE_FAILED")}, &before, at(5), 1, true},
		"teammate, canceled":   {[]store.ConversationTurn{handoff(0, "t1", "TASK_STATE_WORKING"), asked(0), reply(1), handoff(1, "t1", "TASK_STATE_CANCELED")}, &before, at(5), 1, true},
		"teammate, never done": {[]store.ConversationTurn{handoff(0, "t1", "TASK_STATE_WORKING"), asked(0), reply(1)}, &before, at(1).Add(2 * UndeliveredAfter), 1, true},
		"teammate, closing":    {[]store.ConversationTurn{handoff(0, "t1", "TASK_STATE_WORKING"), asked(0), reply(1)}, &before, at(2), 0, false},
		// Its own question to another agent closes mid-turn; only the
		// task it was asked under decides its answer.
		"teammate, own ask inside": {[]store.ConversationTurn{
			handoff(0, "t1", "TASK_STATE_WORKING"), asked(0),
			handoff(1, "t2", "TASK_STATE_WORKING"), handoff(2, "t2", "TASK_STATE_FAILED"),
			reply(3), handoff(3, "t1", "TASK_STATE_COMPLETED"),
		}, &before, at(5), 0, true},
		"mixed": {[]store.ConversationTurn{
			handoff(0, "t1", "TASK_STATE_WORKING"), asked(0), reply(1), handoff(1, "t1", "TASK_STATE_COMPLETED"),
			person, reply(2),
			handoff(3, "t2", "TASK_STATE_WORKING"), asked(3), reply(4), handoff(4, "t2", "TASK_STATE_FAILED"),
		}, &before, at(5), 2, true},
	} {
		t.Run(name, func(t *testing.T) {
			n, settled := UnreadCount(writeConv(t, tc.turns...), tc.read, tc.now)
			if n != tc.want || settled != tc.settled {
				t.Fatalf("UnreadCount = %d, %v; want %d, %v", n, settled, tc.want, tc.settled)
			}
		})
	}
}

func ptr(t time.Time) *time.Time { return &t }
