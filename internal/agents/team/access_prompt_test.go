package team

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func TestYourAccess(t *testing.T) {
	reach := Reach{
		"c-slack": {Key: "slack", Label: "Slack", Accounts: map[string]string{"a1": "@yoga"}},
		"c-loki":  {Key: "loki", Label: "Loki"},
		"c-off":   {Key: "notion", Label: "Notion"},
		"c-plat":  {Key: "notes", Label: "Notes", Tier: TierPlatform},
	}
	grants := []ConnectorGrant{
		{ConnectorID: "c-slack", Level: LevelAll, Accounts: []string{"a1", ""}},
		{ConnectorID: "c-loki", Level: LevelRead},
		{ConnectorID: "c-off", Level: LevelOff},
	}
	got := YourAccess(NewScope(grants, false, false, reach), reach)
	for _, want := range []string{
		"## Your access",
		"- Slack (slack): Write, as @yoga, the bot",
		"- Loki (loki): Read",
		"- Notes (notes): Write",
		"Anything not listed is not available; the owner can change it in Settings.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Notion") {
		t.Errorf("an off connector is listed:\n%s", got)
	}
	// Sorted by name: Loki, Notes, Slack.
	if i, j := strings.Index(got, "Loki"), strings.Index(got, "Slack"); i > j {
		t.Errorf("not sorted:\n%s", got)
	}

	if got := YourAccess(NewScope(nil, false, false, nil), nil); !strings.Contains(got, "could not be read") {
		t.Errorf("nil reach: %s", got)
	}
	if got := YourAccess(DenyAll(), reach); !strings.Contains(got, "You have no connectors.") {
		t.Errorf("deny-all: %s", got)
	}
}

// A large catalog cannot grow the block past its cap (≤40 lines).
func TestYourAccess_Capped(t *testing.T) {
	reach := Reach{}
	for i := 0; i < 100; i++ {
		reach[fmt.Sprintf("c%03d", i)] = ReachItem{Key: "k", Label: fmt.Sprintf("C%03d", i), Tier: TierPlatform}
	}
	got := YourAccess(NewScope(nil, false, false, reach), reach)
	if n := strings.Count(got, "\n") + 1; n > 40 {
		t.Errorf("%d lines, want ≤40", n)
	}
	if !strings.Contains(got, "+70 more") {
		t.Errorf("no overflow line:\n%s", got)
	}
}

func TestSpawnPromptFor(t *testing.T) {
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	svc := NewService(testDB(t), layout)
	off := DefaultFeatures()
	off.Subagents, off.Schedule = false, false
	full := &entity.AgentPersona{OwnerUserID: "u1", Handle: "full", AllowedConnectors: "[]", Features: EncodeFeatures(DefaultFeatures())}
	bare := &entity.AgentPersona{OwnerUserID: "u1", Handle: "bare", AllowedConnectors: "[]", Features: EncodeFeatures(off)}
	for _, p := range []*entity.AgentPersona{full, bare} {
		if err := svc.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []session.CreateOptions{
		{ID: "s-full", AgentID: full.ID},
		{ID: "s-bare", AgentID: bare.ID},
		{ID: "s-child", ParentSessionID: "s-full"},
		{ID: "s-plain"},
	} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}
	sp, ok := svc.SpawnPromptFor(ctx, "s-full")
	if !ok || !sp.Subagents || !sp.Schedule {
		t.Fatalf("full: ok=%v %+v", ok, sp)
	}
	if !strings.Contains(sp.Prompt, "## Who you are") || !strings.Contains(sp.Access, "## Your access") {
		t.Errorf("full: missing blocks: %+v", sp)
	}
	if sp, ok := svc.SpawnPromptFor(ctx, "s-bare"); !ok || sp.Subagents || sp.Schedule {
		t.Errorf("bare: ok=%v %+v", ok, sp)
	}
	for _, id := range []string{"s-child", "s-plain"} {
		if _, ok := svc.SpawnPromptFor(ctx, id); ok {
			t.Errorf("%s: treated as a Team agent's own session", id)
		}
	}
}
