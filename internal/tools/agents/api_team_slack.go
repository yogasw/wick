package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"

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

// Keys of an agent connection row beyond SlackChannelConfig's own.
const (
	agentSlackKeyBotID    = "bot_id"
	agentSlackKeyBotName  = "bot_name"
	agentSlackKeyTeam     = "team_name"
	agentSlackKeyDMMain   = "dm_main_chat"
	agentSlackKeyAppID    = "app_id"
	agentSlackKeyAppToken = "app_config_token"
)

// agentSlackSecretKeys are stored encrypted and never sent back.
var agentSlackSecretKeys = []string{"bot_token", "app_token", "signing_secret", agentSlackKeyAppToken}

// slackAPIURL points auth.test at a stub server in tests; "" = Slack.
var slackAPIURL = ""

// channelWirer gives an agent instance born at runtime the same identity,
// owner and token wiring boot gives every channel. Set by the server.
var channelWirer func(agentchannels.Channel)

// SetChannelWirer is called once by the server after it builds its
// per-channel wiring.
func SetChannelWirer(fn func(agentchannels.Channel)) { channelWirer = fn }

func agentSlackInstanceKey(agentID string) string {
	return agentchannels.AgentSlackType + ":" + agentID
}

// agentSlackSessionPrefix namespaces an agent bot's thread sessions. It
// shares no prefix with "slack-…", so no other Slack instance claims them.
func agentSlackSessionPrefix(agentID string) string { return "slackagent-" + agentID + "-" }

// AgentSlackStatus is GET /api/team/agents/{id}/slack. Secrets are only
// ever reported as set or not.
type AgentSlackStatus struct {
	Connected  bool            `json:"connected"`
	Online     bool            `json:"online"`
	Mode       string          `json:"mode"`
	BotID      string          `json:"bot_id,omitempty"`
	BotName    string          `json:"bot_name,omitempty"`
	TeamName   string          `json:"team_name,omitempty"`
	DMMainChat bool            `json:"dm_main_chat"`
	AppID      string          `json:"app_id,omitempty"`
	Secrets    map[string]bool `json:"secrets"`
	Disabled   bool            `json:"disabled"`
}

func agentSlackStatusOf(p entity.AgentPersona) (AgentSlackStatus, error) {
	m, err := agentchannels.AgentSlackConfig(globalDB, p.ID)
	if err != nil {
		return AgentSlackStatus{}, err
	}
	st := AgentSlackStatus{
		Connected:  m["bot_token"] != "",
		Mode:       firstNonEmpty(m["mode"], "socket"),
		BotID:      m[agentSlackKeyBotID],
		BotName:    m[agentSlackKeyBotName],
		TeamName:   m[agentSlackKeyTeam],
		DMMainChat: m[agentSlackKeyDMMain] != "false",
		AppID:      m[agentSlackKeyAppID],
		Secrets:    map[string]bool{},
		Disabled:   p.Disabled,
	}
	for _, k := range agentSlackSecretKeys {
		st.Secrets[k] = m[k] != ""
	}
	if globalChannels != nil {
		if ch, ok := globalChannels.ChannelByKey(agentSlackInstanceKey(p.ID)).(*agentslack.Channel); ok && ch != nil {
			st.Online = ch.IsConfigured()
		}
	}
	return st, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// apiTeamAgentSlackGet handles GET /api/team/agents/{id}/slack.
func apiTeamAgentSlackGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	st, err := agentSlackStatusOf(p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// agentSlackConnectReq is the wizard's step 2. Empty secrets keep the
// stored value, so the options can be saved without re-pasting tokens.
type agentSlackConnectReq struct {
	Mode           string  `json:"mode"`
	BotToken       string  `json:"bot_token"`
	AppToken       string  `json:"app_token"`
	SigningSecret  string  `json:"signing_secret"`
	AppConfigToken string  `json:"app_config_token"`
	AppID          *string `json:"app_id"`
	DMMainChat     *bool   `json:"dm_main_chat"`
}

// errBotTaken is the answer to a bot token whose app already serves
// another agent or channel: one Slack app = one agent.
type errBotTaken struct{ owner string }

func (e errBotTaken) Error() string {
	return "this Slack app is already connected to " + e.owner + " — create a separate Slack app for each agent"
}

// slackBotIdentity runs auth.test with token.
func slackBotIdentity(token string) (botID, botUserID, botName, teamName string, err error) {
	var opts []slackgo.Option
	if slackAPIURL != "" {
		opts = append(opts, slackgo.OptionAPIURL(slackAPIURL))
	}
	resp, err := slackgo.New(token, opts...).AuthTest()
	if err != nil {
		return "", "", "", "", err
	}
	return resp.BotID, resp.UserID, resp.User, resp.Team, nil
}

// checkBotUnique refuses botID when another agent's connection or any
// running Slack instance already uses it.
func checkBotUnique(agentID, botID, botUserID string) error {
	if botID == "" {
		return nil
	}
	rows, err := agentchannels.ListAgentSlack(globalDB)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Name == agentID {
			continue
		}
		m := map[string]string{}
		_ = json.Unmarshal([]byte(r.Config), &m)
		if m[agentSlackKeyBotID] == botID {
			name := "another agent"
			if other, err := globalTeam.Get(context.Background(), r.Name); err == nil {
				name = "@" + other.Handle
			}
			return errBotTaken{owner: name}
		}
	}
	if globalChannels != nil && botUserID != "" {
		own := agentSlackInstanceKey(agentID)
		for _, ch := range globalChannels.Channels() {
			sc, ok := ch.(*agentslack.Channel)
			if !ok || globalChannels.InstanceKeyOf(ch) == own {
				continue
			}
			if sc.BotUserID() == botUserID {
				return errBotTaken{owner: "a channel on the Channels page"}
			}
		}
	}
	return nil
}

// apiTeamAgentSlackConnect handles PUT /api/team/agents/{id}/slack: checks
// the token with auth.test, refuses a bot another agent uses, stores the
// connection (secrets encrypted) and brings the bot online.
func apiTeamAgentSlackConnect(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var req agentSlackConnectReq
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	m, err := agentchannels.AgentSlackConfig(globalDB, p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	plain := func(k string) string {
		if v := m[k]; v != "" && globalConfigs != nil {
			if d, err := globalConfigs.DecryptSecret(v); err == nil {
				return d
			}
		}
		return m[k]
	}
	mode := firstNonEmpty(strings.TrimSpace(req.Mode), m["mode"], "socket")
	if mode != "socket" && mode != "http" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "mode must be socket or http"})
		return
	}
	secrets := map[string]string{
		"bot_token": strings.TrimSpace(req.BotToken), "app_token": strings.TrimSpace(req.AppToken),
		"signing_secret": strings.TrimSpace(req.SigningSecret), agentSlackKeyAppToken: strings.TrimSpace(req.AppConfigToken),
	}
	for k, v := range secrets {
		if v == "" {
			secrets[k] = plain(k)
		}
	}
	switch {
	case !strings.HasPrefix(secrets["bot_token"], "xoxb-"):
		c.JSON(http.StatusBadRequest, map[string]string{"error": "bot token must start with xoxb-"})
		return
	case mode == "socket" && !strings.HasPrefix(secrets["app_token"], "xapp-"):
		c.JSON(http.StatusBadRequest, map[string]string{"error": "socket mode needs an app token (xapp-…)"})
		return
	case mode == "http" && secrets["signing_secret"] == "":
		c.JSON(http.StatusBadRequest, map[string]string{"error": "HTTP mode needs the signing secret"})
		return
	}
	botID, botUserID, botName, teamName, err := slackBotIdentity(secrets["bot_token"])
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "Slack rejected the bot token: " + err.Error()})
		return
	}
	if err := checkBotUnique(p.ID, botID, botUserID); err != nil {
		status := http.StatusInternalServerError
		if errors.As(err, new(errBotTaken)) {
			status = http.StatusConflict
		}
		c.JSON(status, map[string]string{"error": err.Error()})
		return
	}
	m["mode"] = mode
	m["project_id"] = p.ProjectID
	m[agentSlackKeyBotID], m[agentSlackKeyBotName], m[agentSlackKeyTeam] = botID, botName, teamName
	if req.DMMainChat != nil {
		m[agentSlackKeyDMMain] = strconv.FormatBool(*req.DMMainChat)
	}
	if req.AppID != nil {
		m[agentSlackKeyAppID] = strings.TrimSpace(*req.AppID)
	}
	if globalConfigs != nil {
		m["public_url"] = strings.TrimRight(globalConfigs.AppURL(), "/")
	}
	for k, v := range secrets {
		if v == "" {
			delete(m, k)
			continue
		}
		if globalConfigs != nil {
			enc, err := globalConfigs.EncryptSecret(v)
			if err != nil {
				c.JSON(http.StatusInternalServerError, map[string]string{"error": "encrypt secret: " + err.Error()})
				return
			}
			v = enc
		}
		m[k] = v
	}
	if err := agentchannels.SaveAgentSlack(globalDB, p.ID, p.OwnerUserID, m, true); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	syncAgentSlack(context.Background(), p)
	st, _ := agentSlackStatusOf(p)
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentSlackOptions handles PATCH /api/team/agents/{id}/slack: the
// autosaved options of a connected agent (no token in the body).
func apiTeamAgentSlackOptions(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var req agentSlackConnectReq
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	row, found, err := agentchannels.AgentSlackRow(globalDB, p.ID)
	if err != nil || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent has no Slack connection"})
		return
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, p.ID)
	if req.DMMainChat != nil {
		m[agentSlackKeyDMMain] = strconv.FormatBool(*req.DMMainChat)
	}
	if req.AppID != nil {
		m[agentSlackKeyAppID] = strings.TrimSpace(*req.AppID)
	}
	if v := strings.TrimSpace(req.AppConfigToken); v != "" && globalConfigs != nil {
		enc, err := globalConfigs.EncryptSecret(v)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": "encrypt secret: " + err.Error()})
			return
		}
		m[agentSlackKeyAppToken] = enc
	}
	if err := agentchannels.SaveAgentSlack(globalDB, p.ID, p.OwnerUserID, m, row.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	syncAgentSlack(context.Background(), p)
	st, _ := agentSlackStatusOf(p)
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentSlackDisconnect handles DELETE /api/team/agents/{id}/slack.
func apiTeamAgentSlackDisconnect(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	removeAgentSlack(p.ID)
	c.JSON(http.StatusOK, map[string]string{"status": "disconnected"})
}

// removeAgentSlack takes the agent's bot offline and drops its row. Called
// on disconnect and when the agent (or its project) is deleted.
func removeAgentSlack(agentID string) {
	if globalChannels != nil && globalChannels.HasKey(agentSlackInstanceKey(agentID)) {
		globalChannels.RemoveKeyed(agentSlackInstanceKey(agentID))
	}
	if globalDB != nil {
		if err := agentchannels.DeleteAgentSlack(globalDB, agentID); err != nil {
			log.Warn().Err(err).Str("agent", agentID).Msg("team: delete agent slack connection")
		}
	}
}

// apiTeamAgentSlackHealth handles GET /api/team/agents/{id}/slack/health:
// the live instance's probes plus the feature matrix.
func apiTeamAgentSlackHealth(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var ch *agentslack.Channel
	if globalChannels != nil {
		ch, _ = globalChannels.ChannelByKey(agentSlackInstanceKey(p.ID)).(*agentslack.Channel)
	}
	if ch == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent's Slack bot is not running"})
		return
	}
	checks := ch.HealthCheck()
	if checks == nil {
		checks = []agentchannels.HealthCheck{}
	}
	c.JSON(http.StatusOK, map[string]any{"checks": checks, "matrix": ch.FeatureMatrix(true)})
}

// agentSlackDMMain resolves the main chat a DM to p's bot continues: only
// the owner has one, and only when the connection keeps the option on.
func agentSlackDMMain(agentID string) agentslack.DMMainFn {
	return func(wickUserID string) string {
		p, err := globalTeam.Get(context.Background(), agentID)
		if err != nil || p.Disabled || wickUserID != p.OwnerUserID {
			return ""
		}
		m, err := agentchannels.AgentSlackConfig(globalDB, agentID)
		if err != nil || m[agentSlackKeyDMMain] == "false" {
			return ""
		}
		if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
			return s.ID
		}
		return ""
	}
}

// newAgentSlackInstance builds (not starts) p's bot instance and adds it to
// the registry.
func newAgentSlackInstance(p entity.AgentPersona, store agentchannels.DBStore) *agentslack.Channel {
	cfg, pubURL, err := store.LoadSlackForAgent(p.ID)
	if err != nil || cfg.BotToken == "" {
		return nil
	}
	m, _ := agentchannels.AgentSlackConfig(globalDB, p.ID)
	ch := agentslack.NewWithOwnerCached(cfg, p.OwnerUserID, "", m[agentSlackKeyBotName], m[agentSlackKeyTeam])
	ch.SetSendFunc(globalChannels.SendFuncFor("slack"))
	ch.SetPublicURL(pubURL)
	ch.SetSessionPrefix(agentSlackSessionPrefix(p.ID))
	ch.SetDMMainFn(agentSlackDMMain(p.ID))
	globalChannels.AddKeyed(agentSlackInstanceKey(p.ID), ch, agentslack.NewConfigSourceForAgent(store, ch, p.ID))
	return ch
}

func agentSlackStore() agentchannels.DBStore {
	store := agentchannels.NewDBStore(globalDB)
	store.Configs = globalConfigs
	return store
}

// syncAgentSlack brings p's bot in line with its row and its state: online
// when connected and enabled, gone otherwise. No restart needed.
func syncAgentSlack(ctx context.Context, p entity.AgentPersona) {
	if globalChannels == nil || globalDB == nil {
		return
	}
	key := agentSlackInstanceKey(p.ID)
	row, found, err := agentchannels.AgentSlackRow(globalDB, p.ID)
	if err != nil || !found || !row.Enabled || p.Disabled {
		if globalChannels.HasKey(key) {
			globalChannels.RemoveKeyed(key)
		}
		return
	}
	store := agentSlackStore()
	if globalChannels.HasKey(key) {
		if ch, ok := globalChannels.ChannelByKey(key).(*agentslack.Channel); ok {
			if cfg, pubURL, err := store.LoadSlackForAgent(p.ID); err == nil {
				ch.Reload(ctx, cfg, pubURL)
			}
		}
		return
	}
	ch := newAgentSlackInstance(p, store)
	if ch == nil {
		return
	}
	if channelWirer != nil {
		channelWirer(ch)
	}
	go func() {
		if err := ch.Start(ctx); err != nil {
			log.Warn().Str("instance", key).Err(err).Msg("team: agent slack instance stopped")
		}
	}()
}

// RegisterAgentSlackInstances adds every enabled agent's bot to the
// registry at boot, before the server wires and starts the channels.
func RegisterAgentSlackInstances(ctx context.Context) {
	if globalChannels == nil || globalDB == nil || globalTeam == nil {
		return
	}
	rows, err := agentchannels.ListAgentSlack(globalDB)
	if err != nil {
		log.Warn().Err(err).Msg("team: list agent slack connections")
		return
	}
	store := agentSlackStore()
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		p, err := globalTeam.Get(ctx, r.Name)
		if err != nil || p.Disabled {
			continue
		}
		newAgentSlackInstance(p, store)
	}
}
