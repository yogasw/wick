package agents

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// teamSettingsOf is the GET/PUT answer: every setting in team.SettingFields
// by its wire key, plus what the drawer needs to draw them — the prompt
// limit, and for admins only the link to the page that edits
// `system_prompt_team` for every user (not duplicated here). Those two
// are read-only; PUT refuses them like any unknown key.
func teamSettingsOf(c *tool.Ctx, st entity.TeamSettings) map[string]any {
	out := team.SettingValues(st)
	out["max_prompt_bytes"] = team.MaxTeamPromptBytes
	if u := login.GetUser(c.Context()); u != nil && u.IsAdmin() {
		out["operator_prompt_href"] = c.Base() + "/settings"
	}
	return out
}

// apiTeamSettingsGet handles GET /api/team/settings: the caller's own
// Team settings. There is no user parameter — nobody reads another
// user's.
func apiTeamSettingsGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	st, err := globalTeam.Settings(c.Context(), actorID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, teamSettingsOf(c, st))
}

// apiTeamSettingsSave handles PUT /api/team/settings: patches the caller's
// own row with the settings the body names (any subset, so the drawer can
// autosave one field alone). An unknown key or an invalid value is a 400
// and nothing is saved.
func apiTeamSettingsSave(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	var patch map[string]json.RawMessage
	if err := c.BindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	st, err := globalTeam.Settings(c.Context(), actorID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := team.ApplySettings(&st, patch); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := globalTeam.SaveSettings(c.Context(), &st); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, teamSettingsOf(c, st))
}
