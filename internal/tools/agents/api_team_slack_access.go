package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// agentSlackSettingGroups are the SlackChannelConfig groups an agent's own
// Slack card edits in Team, saved per key on the agent's connection row.
var agentSlackSettingGroups = map[string]bool{
	"Access Control":      true,
	"Agent Behaviour":     true,
	"Reaction Auto-Reply": true,
	"Approval Gates":      true,
}

// agentSlackReadOnlyNotes covers the rest of the Channels page schema
// (Connection, Routing): shown on the Team card read-only, after the four
// editable groups, each with why it is not set there. Every schema field is
// either editable or listed here — TestAgentSlackSettingsCoverSchema.
var agentSlackReadOnlyNotes = map[string]string{
	"mode":           "Set by the connect wizard: an agent's own app runs in socket mode.",
	"bot_token":      "Managed with Replace tokens on this card.",
	"app_token":      "Managed with Replace tokens on this card.",
	"signing_secret": "HTTP mode only — not used by an agent's own app.",
	"project_id":     "Sessions run in the agent's own project.",
	"public_url":     "Set globally in Channels › Slack › Routing.",
}

// Owner identity, stored on the connection row when it is first resolved.
const (
	agentSlackKeyOwnerSlackID     = "owner_slack_id"
	agentSlackKeyOwnerSlackName   = "owner_slack_name"
	agentSlackKeyOwnerSlackHandle = "owner_slack_handle"
)

// agentSlackField is one setting of the Team card: the wick tag of a
// SlackChannelConfig field plus the agent's stored value.
type agentSlackField struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Type        string `json:"type"`
	Options     string `json:"options,omitempty"`
	Desc        string `json:"desc,omitempty"`
	Group       string `json:"group"`
	GroupDesc   string `json:"group_desc,omitempty"`
	VisibleWhen string `json:"visible_when,omitempty"`
	// ReadOnly fields are shown, never saved from Team; Note says why.
	ReadOnly bool   `json:"readonly,omitempty"`
	Note     string `json:"note,omitempty"`
}

// agentSlackSettingFields renders SlackChannelConfig's schema for the Team
// card with the agent's stored values. Same tags as the Channels page; a
// group's description sits on its first field, so it is carried forward.
// The four editable groups come first, then the read-only ones; global
// holds the Channels page values the read-only fields show.
func agentSlackSettingFields(m, global map[string]string) []agentSlackField {
	rows := pkgentity.StructToConfigs(agentconfig.DefaultSlackChannelConfig())
	var edit, ro []agentSlackField
	groupDesc := map[string]string{}
	for _, r := range rows {
		title, desc, _ := strings.Cut(r.Group, "|")
		if desc != "" && groupDesc[title] == "" {
			groupDesc[title] = desc
		}
		f := agentSlackField{
			Key: r.Key, Value: r.Value, Type: r.Type, Options: r.Options, Desc: r.Description,
			Group: title, VisibleWhen: r.VisibleWhen,
		}
		if agentSlackSettingGroups[title] && !r.IsSecret {
			if v, ok := m[r.Key]; ok {
				f.Value = v
			}
			edit = append(edit, f)
			continue
		}
		f.ReadOnly, f.Note, f.VisibleWhen = true, agentSlackReadOnlyNotes[r.Key], ""
		switch {
		case r.IsSecret:
			// Never echoed: only whether it is set.
			f.Value = ""
			if m[r.Key] != "" {
				f.Value = "set"
			}
		case r.Key == "public_url":
			f.Value = global[r.Key]
		default:
			if v, ok := m[r.Key]; ok {
				f.Value = v
			}
		}
		ro = append(ro, f)
	}
	out := append(edit, ro...)
	for i := range out {
		out[i].GroupDesc = groupDesc[out[i].Group]
	}
	return out
}

func agentSlackSettingKey(key string) bool {
	for _, r := range agentSlackSettingFields(nil, nil) {
		if r.Key == key {
			return !r.ReadOnly
		}
	}
	return false
}

// agentSlackOwner resolves the agent owner's Slack user through the agent's
// own bot (users.lookupByEmail, needs users:read.email) and remembers it on
// the row. Email is the only key: "" when the owner has no email, the scope
// is missing or Slack does not know it — a name is never matched instead.
func agentSlackOwner(p entity.AgentPersona, m map[string]string, api *slackgo.Client) (id, name string) {
	if m[agentSlackKeyOwnerSlackID] != "" {
		return m[agentSlackKeyOwnerSlackID], m[agentSlackKeyOwnerSlackName]
	}
	if globalDB == nil || api == nil || p.OwnerUserID == "" {
		return "", ""
	}
	var u entity.User
	if err := globalDB.Select("id", "email").Where("id = ?", p.OwnerUserID).First(&u).Error; err != nil || u.Email == "" {
		return "", ""
	}
	su, err := api.GetUserByEmail(u.Email)
	if err != nil || su == nil {
		log.Debug().Err(err).Str("agent", p.ID).Msg("agents: owner not found in the agent's Slack workspace")
		return "", ""
	}
	name = firstNonEmpty(su.RealName, su.Profile.DisplayName, su.Name, su.ID)
	m[agentSlackKeyOwnerSlackID], m[agentSlackKeyOwnerSlackName], m[agentSlackKeyOwnerSlackHandle] = su.ID, name, su.Name
	return su.ID, name
}

// slackClient is a Slack client for token, pointed at the test stub when set.
func slackClient(token string) *slackgo.Client {
	var opts []slackgo.Option
	if slackAPIURL != "" {
		opts = append(opts, slackgo.OptionAPIURL(slackAPIURL))
	}
	return slackgo.New(token, opts...)
}

// onlyOwnerAccess is the "Only me" default of a newly connected app: a
// preset that fills the users whitelist with the owner alone. With no
// resolved owner the whitelist stays empty — closed, never open to the
// whole workspace — and the card asks for a manual pick.
func onlyOwnerAccess(m map[string]string, ownerID, ownerName string) {
	list := []map[string]string{}
	if ownerID != "" {
		list = append(list, map[string]string{"id": ownerID, "name": ownerName})
	}
	b, _ := json.Marshal(list)
	m["users_mode"] = "whitelist"
	m["allowed_users"] = string(b)
}

// AgentSlackSettings is GET /api/team/agents/{id}/slack/settings.
type AgentSlackSettings struct {
	Fields      []agentSlackField `json:"fields"`
	OwnerID     string            `json:"owner_slack_id,omitempty"`
	OwnerName   string            `json:"owner_slack_name,omitempty"`
	OwnerHandle string            `json:"owner_slack_handle,omitempty"`
}

func agentSlackSettingsOf(p entity.AgentPersona) (AgentSlackSettings, bool) {
	row, found, err := agentchannels.AgentSlackRow(globalDB, p.ID)
	if err != nil || !found {
		return AgentSlackSettings{}, false
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, p.ID)
	if m[agentSlackKeyOwnerSlackID] == "" {
		if ch := agentSlackLive(p.ID); ch != nil && ch.API() != nil {
			if id, _ := agentSlackOwner(p, m, ch.API()); id != "" {
				_ = agentchannels.SaveAgentSlack(globalDB, p.ID, p.OwnerUserID, m, row.Enabled)
			}
		}
	}
	global, _ := agentchannels.GetChannelConfigMap(globalDB, "slack")
	fields := agentSlackSettingFields(m, global)
	for i := range fields {
		if fields[i].Key == "project_id" && fields[i].Value != "" && globalMgr != nil {
			if proj, ok := globalMgr.Registry().Project(fields[i].Value); ok && proj.Meta.Name != "" {
				fields[i].Value = proj.Meta.Name
			}
		}
	}
	return AgentSlackSettings{
		Fields:      fields,
		OwnerID:     m[agentSlackKeyOwnerSlackID],
		OwnerName:   m[agentSlackKeyOwnerSlackName],
		OwnerHandle: m[agentSlackKeyOwnerSlackHandle],
	}, true
}

func agentSlackLive(agentID string) *agentslack.Channel {
	if globalChannels == nil {
		return nil
	}
	ch, _ := globalChannels.ChannelByKey(agentSlackInstanceKey(agentID)).(*agentslack.Channel)
	return ch
}

// apiTeamAgentSlackSettingsGet handles GET /api/team/agents/{id}/slack/settings.
func apiTeamAgentSlackSettingsGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	st, found := agentSlackSettingsOf(p)
	if !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent has no Slack connection"})
		return
	}
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentSlackSettingsPatch handles PATCH /api/team/agents/{id}/slack/settings:
// one autosaved key of the four setting groups.
func apiTeamAgentSlackSettingsPatch(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if !agentSlackSettingKey(req.Key) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown setting " + req.Key})
		return
	}
	row, found, err := agentchannels.AgentSlackRow(globalDB, p.ID)
	if err != nil || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent has no Slack connection"})
		return
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, p.ID)
	m[req.Key] = req.Value
	if err := agentchannels.SaveAgentSlack(globalDB, p.ID, p.OwnerUserID, m, row.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	syncAgentSlack(context.Background(), p)
	st, _ := agentSlackSettingsOf(p)
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentSlackLookup handles GET /api/team/agents/{id}/slack/lookup?source=&q=:
// the pickers of the settings groups, answered by the agent's own bot.
func apiTeamAgentSlackLookup(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	ch := agentSlackLive(p.ID)
	if ch == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "the agent's Slack app is not online"})
		return
	}
	items, err := ch.Lookup(c.R.URL.Query().Get("source"), c.R.URL.Query().Get("q"))
	if err != nil {
		c.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if items == nil {
		items = []agentchannels.LookupItem{}
	}
	c.JSON(http.StatusOK, map[string]any{"items": items})
}
