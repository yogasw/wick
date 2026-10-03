package agents

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
)

func decodeTeamSettings(t *testing.T, body string) teamSettingsDTO {
	t.Helper()
	var d teamSettingsDTO
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
	if d := decodeTeamSettings(t, w.Body.String()); w.Code != http.StatusOK || d.Prompt != "" || !d.OpenTeam || d.MaxPromptBytes != team.MaxTeamPromptBytes {
		t.Fatalf("defaults: %d %s", w.Code, w.Body)
	}

	w, c = teamReq(t, u1, http.MethodPut, "/api/team/settings", map[string]any{"prompt": "U1 RULES"}, nil)
	apiTeamSettingsSave(c)
	if w.Code != http.StatusOK {
		t.Fatalf("save prompt: %d %s", w.Code, w.Body)
	}
	w, c = teamReq(t, u1, http.MethodPut, "/api/team/settings", map[string]any{"open_team": false}, nil)
	apiTeamSettingsSave(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "U1 RULES" || d.OpenTeam {
		t.Fatalf("toggle lost the prompt or did not stick: %s", w.Body)
	}

	w, c = teamReq(t, u2, http.MethodGet, "/api/team/settings", nil, nil)
	apiTeamSettingsGet(c)
	if d := decodeTeamSettings(t, w.Body.String()); d.Prompt != "" || !d.OpenTeam {
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
