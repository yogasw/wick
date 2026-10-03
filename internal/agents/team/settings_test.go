package team

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// A user who never saved gets the defaults; a save is read back, a second
// save updates the same row, and one user's row is not another's.
func TestTeamSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := NewStore(testDB(t))

	got, err := st.Settings(ctx, "u1")
	if err != nil || got.Prompt != "" || !got.OpenTeam() {
		t.Fatalf("defaults: %+v err=%v", got, err)
	}
	if err := st.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1", Prompt: "Answer in Indonesian.", ClassicHome: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1", Prompt: "Be brief."}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Settings(ctx, "u1")
	if got.Prompt != "Be brief." || !got.OpenTeam() {
		t.Fatalf("after update: %+v", got)
	}
	if other, _ := st.Settings(ctx, "u2"); other.Prompt != "" {
		t.Fatalf("u2 sees u1's prompt: %+v", other)
	}
}

func TestTeamSettingsPromptLimit(t *testing.T) {
	st := NewStore(testDB(t))
	if err := st.SaveSettings(context.Background(), &entity.TeamSettings{UserID: "u1", Prompt: strings.Repeat("a", MaxTeamPromptBytes+1)}); err == nil {
		t.Fatal("a prompt over the limit was saved")
	}
	if err := ValidateTeamPrompt(strings.Repeat("a", MaxTeamPromptBytes)); err != nil {
		t.Fatalf("a prompt at the limit was refused: %v", err)
	}
}

// The Team instructions are the agent OWNER's, and only the agent's own
// session carries them.
func TestSpawnPromptForTeamInstructions(t *testing.T) {
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	svc := NewService(testDB(t), layout)
	mine := &entity.AgentPersona{OwnerUserID: "u1", Handle: "mine", AllowedConnectors: "[]"}
	theirs := &entity.AgentPersona{OwnerUserID: "u2", Handle: "theirs", AllowedConnectors: "[]"}
	for _, p := range []*entity.AgentPersona{mine, theirs} {
		if err := svc.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1", Prompt: "U1-TEAM-RULES"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []session.CreateOptions{
		{ID: "s-mine", AgentID: mine.ID},
		{ID: "s-theirs", AgentID: theirs.ID},
	} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}
	if sp, ok := svc.SpawnPromptFor(ctx, "s-mine"); !ok || sp.TeamInstructions != "U1-TEAM-RULES" {
		t.Errorf("owner's agent: ok=%v instructions=%q", ok, sp.TeamInstructions)
	}
	if sp, ok := svc.SpawnPromptFor(ctx, "s-theirs"); !ok || sp.TeamInstructions != "" {
		t.Errorf("another owner's agent got u1's instructions: %q", sp.TeamInstructions)
	}
}

// Every registered setting is on the wire, and ApplySettings is all or
// nothing: an unknown key or a bad value leaves the row as it was.
func TestApplySettingsRegistry(t *testing.T) {
	st := entity.TeamSettings{UserID: "u1", Prompt: "old"}
	vals := SettingValues(st)
	for _, f := range SettingFields {
		if _, ok := vals[f.Key]; !ok {
			t.Errorf("setting %q missing from SettingValues", f.Key)
		}
	}
	if len(vals) != len(SettingFields) {
		t.Errorf("SettingValues has %d keys, registry %d", len(vals), len(SettingFields))
	}

	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	if err := ApplySettings(&st, map[string]json.RawMessage{"prompt": raw(`"new"`), "nope": raw(`1`)}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("unknown key: err=%v", err)
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"prompt": raw(`"new"`), "open_team": raw(`"yes"`)}); err == nil {
		t.Fatal("mistyped open_team accepted")
	}
	if st.Prompt != "old" || st.ClassicHome {
		t.Fatalf("refused patch changed the row: %+v", st)
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"prompt": raw(`"new"`), "open_team": raw(`false`)}); err != nil {
		t.Fatal(err)
	}
	if st.Prompt != "new" || st.OpenTeam() {
		t.Fatalf("patch not applied: %+v", st)
	}
}
