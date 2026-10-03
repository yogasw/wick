package agents

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// teamSettingsDTO is the Team settings drawer's view of the caller's own
// row. OperatorPromptHref is set for admins only: the link to the page
// that edits `system_prompt_team` for every user, which this drawer does
// not duplicate.
type teamSettingsDTO struct {
	Prompt             string `json:"prompt"`
	OpenTeam           bool   `json:"open_team"`
	MaxPromptBytes     int    `json:"max_prompt_bytes"`
	OperatorPromptHref string `json:"operator_prompt_href,omitempty"`
}

// teamSettingsWriteReq is the PUT body. Every field is optional so the
// drawer can autosave one field without echoing the other.
type teamSettingsWriteReq struct {
	Prompt   *string `json:"prompt"`
	OpenTeam *bool   `json:"open_team"`
}

func teamSettingsOf(c *tool.Ctx, st entity.TeamSettings) teamSettingsDTO {
	dto := teamSettingsDTO{Prompt: st.Prompt, OpenTeam: st.OpenTeam(), MaxPromptBytes: team.MaxTeamPromptBytes}
	if u := login.GetUser(c.Context()); u != nil && u.IsAdmin() {
		dto.OperatorPromptHref = c.Base() + "/settings"
	}
	return dto
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
// own row with the fields the body names.
func apiTeamSettingsSave(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	var req teamSettingsWriteReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Prompt != nil {
		if err := team.ValidateTeamPrompt(*req.Prompt); err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	st, err := globalTeam.Settings(c.Context(), actorID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if req.Prompt != nil {
		st.Prompt = *req.Prompt
	}
	if req.OpenTeam != nil {
		st.ClassicHome = !*req.OpenTeam
	}
	if err := globalTeam.SaveSettings(c.Context(), &st); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, teamSettingsOf(c, st))
}
