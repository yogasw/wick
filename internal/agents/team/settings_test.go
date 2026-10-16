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
	if err != nil || got.Prompt != "" || got.OpenTeam() {
		t.Fatalf("defaults: %+v err=%v", got, err)
	}
	if err := st.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1", Prompt: "Answer in Indonesian.", TeamHome: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1", Prompt: "Be brief."}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Settings(ctx, "u1")
	if got.Prompt != "Be brief." || got.OpenTeam() {
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
	vals := SettingValues(st, SettingEnv{})
	for _, f := range SettingFields {
		if _, ok := vals[f.Key]; !ok {
			t.Errorf("setting %q missing from SettingValues", f.Key)
		}
	}
	if len(vals) != len(SettingFields) {
		t.Errorf("SettingValues has %d keys, registry %d", len(vals), len(SettingFields))
	}

	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	if err := ApplySettings(&st, map[string]json.RawMessage{"prompt": raw(`"new"`), "nope": raw(`1`)}, SettingEnv{}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("unknown key: err=%v", err)
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"prompt": raw(`"new"`), "open_team": raw(`"yes"`)}, SettingEnv{}); err == nil {
		t.Fatal("mistyped open_team accepted")
	}
	if st.Prompt != "old" || st.TeamHome {
		t.Fatalf("refused patch changed the row: %+v", st)
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"prompt": raw(`"new"`), "open_team": raw(`false`)}, SettingEnv{}); err != nil {
		t.Fatal(err)
	}
	if st.Prompt != "new" || st.OpenTeam() {
		t.Fatalf("patch not applied: %+v", st)
	}
	if !st.IdleAnimations() {
		t.Fatal("idle animations should default on")
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"idle_animations": raw(`"off"`)}, SettingEnv{}); err == nil {
		t.Fatal("mistyped idle_animations accepted")
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"idle_animations": raw(`false`)}, SettingEnv{}); err != nil {
		t.Fatal(err)
	}
	if st.IdleAnimations() || SettingValues(st, SettingEnv{})["idle_animations"] != false {
		t.Fatalf("idle_animations not applied: %+v", st)
	}
}

// ticket_projects: a save keeps only projects the caller may open with
// ticket mode on (any other id refuses the whole patch), collapses
// duplicates in order, and a read hides projects that stopped passing.
func TestTicketProjectsSetting(t *testing.T) {
	allowed := map[string]bool{"p1": true, "p2": true, "p3": true}
	env := SettingEnv{TicketProject: func(id string) bool { return allowed[id] }}
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	st := entity.TeamSettings{UserID: "u1"}

	if got := SettingValues(st, env)["ticket_projects"].([]string); len(got) != 0 {
		t.Fatalf("default selection: %v", got)
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(`["p2","p1","p2"," p1 ",""]`)}, env); err != nil {
		t.Fatal(err)
	}
	if st.TicketProjects != `["p2","p1"]` {
		t.Fatalf("stored: %q", st.TicketProjects)
	}

	// Not openable, tickets off, unknown: all the same to the caller.
	for _, bad := range []string{`["p1","hidden"]`, `["ghost"]`} {
		err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(bad), "open_team": raw(`true`)}, env)
		if err == nil {
			t.Fatalf("%s accepted", bad)
		}
		if st.TicketProjects != `["p2","p1"]` || st.TeamHome {
			t.Fatalf("refused patch changed the row: %+v", st)
		}
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(`"p1"`)}, env); err == nil {
		t.Fatal("non-array accepted")
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(`["p1"]`)}, SettingEnv{}); err == nil {
		t.Fatal("nil gate allowed a project")
	}
	err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(`["x1","x2","x3","x4","x5","x6","x7"]`)}, env)
	if err == nil || !strings.Contains(err.Error(), "x5 and 2 more") || strings.Contains(err.Error(), "x6") {
		t.Fatalf("long refusal not trimmed: %v", err)
	}
	many, _ := json.Marshal(make([]string, 2*MaxTicketProjects+1))
	if err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": many}, env); err == nil {
		t.Fatal("oversized list accepted")
	}
	// No selection: the gate is never asked.
	asked := false
	lazy := SettingEnv{TicketProject: func(string) bool { asked = true; return true }}
	SettingValues(entity.TeamSettings{UserID: "u9"}, lazy)
	if asked {
		t.Fatal("gate consulted with nothing selected")
	}

	// p2 loses access (or its ticket mode goes off): hidden, still stored.
	delete(allowed, "p2")
	got := SettingValues(st, env)["ticket_projects"].([]string)
	if strings.Join(got, ",") != "p1" {
		t.Fatalf("visible: %v", got)
	}
	if st.TicketProjects != `["p2","p1"]` {
		t.Fatalf("read rewrote the row: %q", st.TicketProjects)
	}

	if err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(`[]`)}, env); err != nil || st.TicketProjects != "" {
		t.Fatalf("clear: %q err=%v", st.TicketProjects, err)
	}
	if err := ApplySettings(&st, map[string]json.RawMessage{"ticket_projects": raw(`null`)}, env); err != nil || st.TicketProjects != "" {
		t.Fatalf("null: %q err=%v", st.TicketProjects, err)
	}
}

// The selection survives the store round trip (the column is migrated).
func TestTicketProjectsStored(t *testing.T) {
	ctx := context.Background()
	s := NewStore(testDB(t))
	if err := s.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1", TicketProjects: `["p1","p2"]`}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Settings(ctx, "u1")
	if err != nil || strings.Join(got.TicketProjectIDs(), ",") != "p1,p2" {
		t.Fatalf("read back: %+v err=%v", got, err)
	}

	// Clearing writes the empty value over the stored one.
	if err := s.SaveSettings(ctx, &entity.TeamSettings{UserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Settings(ctx, "u1")
	if err != nil || got.TicketProjects != "" || len(got.TicketProjectIDs()) != 0 {
		t.Fatalf("after clear: %+v err=%v", got, err)
	}
}
