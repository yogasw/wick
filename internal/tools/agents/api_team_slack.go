package agents

import (
	"net/http"
	"strings"

	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/team"
	slackconnector "github.com/yogasw/wick/internal/connectors/slack"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// agentSlackManifest builds the Custom-mode Slack app manifest of p: its
// name, description and colour from the agent, scopes and events from the
// channel's requirement table.
func agentSlackManifest(p entity.AgentPersona) agentslack.Manifest {
	in := agentslack.ManifestInput{
		Name:             p.Handle,
		Color:            team.DecodeAvatar(p.Avatar).Color,
		SuggestedPrompts: slackPrompts(team.DecodeSuggestedPrompts(p.SuggestedPrompts)),
		UserScopes:       slackconnector.UserOAuthScopeList(),
	}
	if p.ProjectID != "" && globalMgr != nil {
		if proj, ok := globalMgr.Registry().Project(p.ProjectID); ok {
			if proj.Meta.Name != "" {
				in.Name = proj.Meta.Name
			}
			in.Description = proj.Meta.Description
			in.AgentDescription = proj.Meta.Description
		}
	}
	if p.Tagline != "" {
		in.Description = strings.TrimSpace(p.Tagline + " — " + in.Description)
		in.Description = strings.TrimSuffix(in.Description, " —")
	}
	if globalConfigs != nil {
		in.PublicURL = globalConfigs.AppURL()
	}
	return agentslack.GenerateManifest(in)
}

// apiTeamAgentSlackManifest handles GET /api/team/agents/{id}/slack/manifest:
// the manifest JSON plus the api.slack.com link that opens "Create app"
// with it prefilled.
func apiTeamAgentSlackManifest(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	m := agentSlackManifest(p)
	link, err := agentslack.ManifestCreateURL(m)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"manifest": m, "create_url": link})
}

func slackPrompts(in []team.SuggestedPrompt) []agentslack.SuggestedPrompt {
	out := make([]agentslack.SuggestedPrompt, 0, len(in))
	for _, p := range in {
		out = append(out, agentslack.SuggestedPrompt{Title: p.Title, Message: p.Message})
	}
	return out
}
