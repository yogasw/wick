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
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
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

func TestRefusalTurn(t *testing.T) {
	hop := refusalTurn(teamlink.Refusal{From: "captain", To: "anton", ContextID: "c1", HopLimit: true, Err: teamlink.ErrHopLimit}, time.Unix(1, 0))
	if hop.Kind != store.KindHopLimit || hop.Role != "system" || hop.Extras["context_id"] != "c1" || hop.Extras["max_turns"] != "4" {
		t.Fatalf("hop = %+v", hop)
	}
	ref := refusalTurn(teamlink.Refusal{From: "captain", To: "sleepy", Err: teamlink.ErrUnknownHandle}, time.Unix(1, 0))
	if ref.Kind != store.KindMentionRefused || ref.Text != "@sleepy doesn't take mentions" || ref.Extras["reason"] == "" {
		t.Fatalf("refused = %+v", ref)
	}
}

func TestAgentCreatedTextAndExtras(t *testing.T) {
	if got := agentCreatedText("Rekap", "Yoga", "", "Notion (read)"); got != "Rekap joined the team · created by Yoga · Notion (read)" {
		t.Fatalf("text = %q", got)
	}
	if got := agentCreatedText("Rekap", "", "Yoga", ""); got != "Rekap joined the team · approved by Yoga" {
		t.Fatalf("text = %q", got)
	}
	p := entity.AgentPersona{ID: "a1", Handle: "rekap", AllowedConnectors: `[{"connector_id":"n","level":"read"}]`}
	ex := agentCreatedExtras(p, "Rekap", "Yoga", "", func(id string) string { return "Notion" }, "wizard")
	if ex["agent_id"] != "a1" || ex["handle"] != "rekap" || ex["grants_summary"] != "Notion (read)" || ex["via"] != "wizard" || ex["created_by"] != "Yoga" {
		t.Fatalf("extras = %v", ex)
	}
}

func TestStampSpeakers(t *testing.T) {
	turns := []store.ConversationTurn{
		{Role: "user", Source: "ui"}, {Role: "assistant"},
		{Role: "user", Source: sourceTeam}, {Role: "assistant"},
		{Role: "system", Kind: store.KindHopLimit}, {Role: "assistant"},
		{Role: "user", Source: "slack"}, {Role: "assistant"},
	}
	stampSpeakers(turns, &store.Speaker{AgentID: "a1", Handle: "anton"})
	want := []string{"", store.ViaDirect, "", store.ViaMention, "", store.ViaMention, "", store.ViaDirect}
	for i, w := range want {
		sp := turns[i].Speaker
		if w == "" {
			if sp != nil {
				t.Fatalf("turn %d (%s) got speaker %+v", i, turns[i].Role, sp)
			}
			continue
		}
		if sp == nil || sp.Via != w || sp.AgentID != "a1" || sp.Handle != "anton" {
			t.Fatalf("turn %d speaker = %+v, want via %s", i, sp, w)
		}
	}
	plain := []store.ConversationTurn{{Role: "assistant"}}
	stampSpeakers(plain, nil)
	if plain[0].Speaker != nil {
		t.Fatal("ordinary session got a speaker")
	}
}
