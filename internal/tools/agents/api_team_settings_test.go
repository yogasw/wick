package agents

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

// teamSettingsWire is the GET/PUT answer as the drawer reads it.
type teamSettingsWire struct {
	Prompt             string `json:"prompt"`
	OpenTeam           bool   `json:"open_team"`
	IdleAnimations     bool   `json:"idle_animations"`
	MaxPromptBytes     int    `json:"max_prompt_bytes"`
	OperatorPromptHref string `json:"operator_prompt_href"`
}

func decodeTeamSettings(t *testing.T, body string) teamSettingsWire {
	t.Helper()
	var d teamSettingsWire
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return d
}

// GET answers the defaults, a PUT naming one field keeps the other, and
// what one user saves is not what another reads.
func TestTeamSettingsAPIOwnRowOnly(t *testing.T) {
	withTeamWorld(t)
	u1, u2 := &entity.User{ID: "u1"}, &entity.User{ID: "u2"}

	w, c := teamReq(t, u1, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if d := decodeTeamSettings(t, w.Body.String()); w.Code != http.StatusOK || d.Prompt != "" || d.OpenTeam || !d.IdleAnimations || d.MaxPromptBytes != team.MaxTeamPromptBytes {
		t.Fatalf("defaults: %d %s", w.Code, w.Body)
	}

	w, c = teamReq(t, u1, http.MethodPut, "/api/team/settings", map[string]any{"prompt": "U1 RULES"}, nil)
	apiTeamSettingsSave(c)
	if w.Code != http.StatusOK {
		t.Fatalf("save prompt: %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, u1, http.MethodPut, "/api/team/settings", map[string]any{"open_team": true}, nil)
	apiTeamSettingsSave(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "U1 RULES" || !d.OpenTeam {
		t.Fatalf("toggle on lost the prompt or did not stick: %s", w.Body)
	}
	w, c = teamReq(t, u1, http.MethodPut, "/api/team/settings", map[string]any{"open_team": false}, nil)
	apiTeamSettingsSave(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "U1 RULES" || d.OpenTeam {
		t.Fatalf("toggle off lost the prompt or did not stick: %s", w.Body)
	}

	w, c = teamReq(t, u1, http.MethodPut, "/api/team/settings", map[string]any{"idle_animations": false}, nil)
	apiTeamSettingsSave(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "U1 RULES" || d.OpenTeam || d.IdleAnimations {
		t.Fatalf("idle animations off did not stick: %s", w.Body)
	}

	w, c = teamReq(t, u2, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "" || d.OpenTeam || !d.IdleAnimations {
		t.Fatalf("u2 reads u1's settings: %s", w.Body)
	}
	if d := decodeTeamSettings(t, w.Body.String()); d.OperatorPromptHref != "" {
		t.Fatalf("non-admin got the operator prompt link: %s", w.Body)
	}
}

func TestTeamSettingsAPIRejectsLongPrompt(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	w, c := teamReq(t, u, http.MethodPut, "/api/team/settings", map[string]any{"prompt": strings.Repeat("x", team.MaxTeamPromptBytes+1)}, nil)
	apiTeamSettingsSave(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
	w, c = teamReq(t, u, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "" {
		t.Fatal("refused prompt was stored")
	}
}

func TestTeamSettingsAPINeedsSignIn(t *testing.T) {
	withTeamWorld(t)
	w, c := teamReq(t, &entity.User{}, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", w.Code)
	}
}

// Admins get the link to the operator prompt page instead of a second
// editor for it.
func TestTeamSettingsAPIAdminLink(t *testing.T) {
	withTeamWorld(t)
	w, c := teamReq(t, &entity.User{ID: "a1", Role: entity.RoleAdmin}, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.OperatorPromptHref != "/tools/agents/settings" {
		t.Fatalf("admin link = %q", d.OperatorPromptHref)
	}
}

// PUT takes only registered settings with the right type, all or nothing:
// one bad key refuses the whole body.
func TestTeamSettingsAPIRejectsUnknownAndMistyped(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	for name, body := range map[string]map[string]any{
		"unknown key":         {"prompt": "kept?", "colour": "red"},
		"read-only meta":      {"max_prompt_bytes": 1},
		"wrong type for bool": {"open_team": "no"},
		"wrong type for text": {"prompt": 42},
	} {
		w, c := teamReq(t, u, http.MethodPut, "/api/team/settings", body, nil)
		apiTeamSettingsSave(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, w.Code)
		}
	}
	w, c := teamReq(t, u, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "" || d.OpenTeam {
		t.Fatalf("a refused body was partly saved: %s", w.Body)
	}
}
