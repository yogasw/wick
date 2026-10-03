package agents

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
)

func TestRecordSystemTurnWritesPublishesAndTouches(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	b := NewBroadcaster()
	ch, unsub := b.Subscribe("S1")
	defer unsub()
	turn := systemTurn(store.KindHopLimit, "hop limit", map[string]string{"context_id": "c1"}, time.Unix(100, 0))
	recordSystemTurn(layout, b, "S1", turn)

	raw, err := os.ReadFile(layout.SessionConversation("S1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"kind":"hop_limit"`) || !strings.Contains(string(raw), `"role":"system"`) {
		t.Fatalf("thread = %s", raw)
	}
	select {
	case ev := <-ch:
		var got store.ConversationTurn
		if ev.Type != evSystemEvent || json.Unmarshal([]byte(ev.Data), &got) != nil || got.Kind != store.KindHopLimit || got.Extras["context_id"] != "c1" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no system_event")
	}
	// Only mention_handoff is mirrored under its legacy name.
	select {
	case ev := <-ch:
		t.Fatalf("unexpected second event %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestRecordSystemTurnNoSessionIsNoop(t *testing.T) {
	recordSystemTurn(config.Layout{}, nil, "S1", systemTurn("x", "", nil, time.Now()))
	recordSystemTurn(config.Layout{BaseDir: t.TempDir()}, nil, "", systemTurn("x", "", nil, time.Now()))
}

func TestGrantsSummaryAndDiff(t *testing.T) {
	label := func(id string) string { return map[string]string{"n": "Notion", "s": "Slack", "l": "Loki"}[id] }
	before := []team.ConnectorGrant{{ConnectorID: "s", Level: team.LevelAll}, {ConnectorID: "l", Level: team.LevelRead}}
	after := []team.ConnectorGrant{{ConnectorID: "n", Level: "weird"}, {ConnectorID: "l", Level: team.LevelAll}, {ConnectorID: "x", Level: team.LevelOff}}
	if got := grantsSummary(after, label); got != "Notion (read), Loki (all)" {
		t.Fatalf("summary = %q", got)
	}
	got := strings.Join(grantsDiff(before, after, label), " | ")
	if got != "+Notion (read) | -Slack | Loki: read → all" {
		t.Fatalf("diff = %q", got)
	}
	if d := grantsDiff(before, before, label); len(d) != 0 {
		t.Fatalf("no change, diff = %v", d)
	}
}
