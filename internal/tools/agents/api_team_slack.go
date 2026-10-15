package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

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

	// Token rotation (xoxe.xapp- app tokens), see SlackChannelConfig.
	agentSlackKeyRefresh   = "app_refresh_token"
	agentSlackKeyClientID  = "client_id"
	agentSlackKeyClientSec = "client_secret"
	agentSlackKeyExpiresAt = "app_token_expires_at"
)

// agentSlackSecretKeys are stored encrypted and never sent back.
var agentSlackSecretKeys = []string{"bot_token", "app_token", "signing_secret", agentSlackKeyAppToken,
	agentSlackKeyRefresh, agentSlackKeyClientSec}

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

	// Rotating is true for an xoxe.xapp- app token, which wick refreshes
	// itself. ClientID is not a secret and is sent back as is.
	Rotating          bool   `json:"rotating"`
	ClientID          string `json:"client_id,omitempty"`
	AppTokenExpiresAt string `json:"app_token_expires_at,omitempty"`
	LastRefresh       string `json:"last_refresh,omitempty"`
	LastError         string `json:"last_error,omitempty"`
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
	st.ClientID = m[agentSlackKeyClientID]
	st.AppTokenExpiresAt = m[agentSlackKeyExpiresAt]
	// The stored app token is encrypted, so whether it rotates is read
	// from its plaintext — never sent back.
	appToken := m["app_token"]
	if appToken != "" && globalConfigs != nil {
		if d, err := globalConfigs.DecryptSecret(appToken); err == nil {
			appToken = d
		}
	}
	st.Rotating = agentslack.IsRotatingAppToken(appToken)
	if globalChannels != nil {
		if ch, ok := globalChannels.ChannelByKey(agentSlackInstanceKey(p.ID)).(*agentslack.Channel); ok && ch != nil {
			st.Online = ch.IsConfigured()
			rot := ch.AppTokenRotation()
			if rot.Rotating {
				st.Rotating = true
				if !rot.ExpiresAt.IsZero() {
					st.AppTokenExpiresAt = rot.ExpiresAt.UTC().Format(time.RFC3339)
				}
				if !rot.LastRefresh.IsZero() {
					st.LastRefresh = rot.LastRefresh.UTC().Format(time.RFC3339)
				}
				st.LastError = rot.LastError
			}
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
	// Needed with a rotating (xoxe.xapp-) app token. Empty keeps the
	// stored value, like the secrets above.
	AppRefreshToken string `json:"app_refresh_token"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
}

// errBotTaken is the answer to a bot token whose app already serves
// another agent or channel: one Slack app = one agent.
type errBotTaken struct{ owner string }

func (e errBotTaken) Error() string {
	return "this Slack app is already connected to " + e.owner + " — create a separate Slack app for each agent"
}

// slackBotIdentity runs auth.test with token.
func slackBotIdentity(token string) (botID, botUserID, botName, teamName string, err error) {
	resp, err := slackClient(token).AuthTest()
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
		agentSlackKeyRefresh: strings.TrimSpace(req.AppRefreshToken), agentSlackKeyClientSec: strings.TrimSpace(req.ClientSecret),
	}
	newAppToken := secrets["app_token"] != "" && secrets["app_token"] != plain("app_token")
	for k, v := range secrets {
		if v == "" {
			secrets[k] = plain(k)
		}
	}
	clientID := firstNonEmpty(strings.TrimSpace(req.ClientID), m[agentSlackKeyClientID])
	if newAppToken {
		if agentslack.IsRotatingAppToken(secrets["app_token"]) {
			// The stored refresh token belongs to the previous app token:
			// a new rotating one only works with the refresh token issued
			// with it.
			if strings.TrimSpace(req.AppRefreshToken) == "" {
				c.JSON(http.StatusBadRequest, map[string]string{"error": "a new rotating app token (xoxe.xapp-…) must be pasted together with its refresh token (xoxe-1-…)"})
				return
			}
		} else {
			// Back to a token that does not rotate: drop the rotation
			// settings so nothing claims a refresh token is held.
			secrets[agentSlackKeyRefresh], secrets[agentSlackKeyClientSec] = "", ""
			clientID = ""
			delete(m, agentSlackKeyClientID)
		}
	}
	if msg := agentSlackTokenError(mode, secrets["bot_token"], secrets["app_token"], secrets["signing_secret"],
		secrets[agentSlackKeyRefresh], clientID, secrets[agentSlackKeyClientSec]); msg != "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": msg})
		return
	}
	if _, _, instant, err := instantRow(globalDB, p.ID); err == nil && instant {
		c.JSON(http.StatusConflict, map[string]string{"error": "this agent answers through the shared Slack app (Instant mode) — turn that off before connecting its own app"})
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
	// A brand-new app starts as "Only me": the owner alone may use it, or
	// nobody until someone is picked when the owner is not found by email.
	// Connections that already exist keep whatever access they had.
	if m["bot_token"] == "" && m["users_mode"] == "" {
		id, name := agentSlackOwner(p, m, slackClient(secrets["bot_token"]))
		onlyOwnerAccess(m, id, name)
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
	if clientID != "" {
		m[agentSlackKeyClientID] = clientID
	}
	if newAppToken {
		// A pasted app token starts a new rotation lineage: the expiry
		// stored for the previous one no longer applies.
		delete(m, agentSlackKeyExpiresAt)
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

// agentSlackTokenError validates a connection's tokens, "" when they are
// usable: the bot token (xoxb- or xoxe.xoxb-), the app token for socket
// mode (xapp-, or xoxe.xapp- with refresh token + client ID + secret), the
// signing secret for HTTP mode.
func agentSlackTokenError(mode, botToken, appToken, signingSecret, refreshToken, clientID, clientSecret string) string {
	switch {
	case !agentslack.IsBotToken(botToken):
		return "bot token must start with xoxb- (or xoxe.xoxb- when token rotation is on)"
	case mode == "socket" && !agentslack.IsAppToken(appToken):
		return "socket mode needs an app token (xapp-… or xoxe.xapp-…)"
	case mode == "http" && signingSecret == "":
		return "HTTP mode needs the signing secret"
	}
	if mode != "socket" || !agentslack.IsRotatingAppToken(appToken) {
		return ""
	}
	var miss []string
	if !strings.HasPrefix(refreshToken, "xoxe-") {
		miss = append(miss, "the refresh token (xoxe-1-…)")
	}
	if clientID == "" {
		miss = append(miss, "the Client ID")
	}
	if clientSecret == "" {
		miss = append(miss, "the Client Secret")
	}
	if len(miss) == 0 {
		return ""
	}
	return "a rotating app token (xoxe.xapp-…) also needs " + strings.Join(miss, ", ") +
		" — find them under Basic Information → App Credentials and App-Level Tokens"
}

// persistAgentSlackAppToken stores the app token pair agentID's bot just
// rotated on its connection row, secrets encrypted, merged under a row lock
// so a concurrent options save cannot undo it.
func persistAgentSlackAppToken(agentID string) agentslack.PersistAppTokenFn {
	return func(accessToken, refreshToken string, expiresAt time.Time) error {
		if globalDB == nil {
			return errors.New("db not ready")
		}
		kv, err := agentSlackStore().SlackAppTokenKeys(accessToken, refreshToken, expiresAt)
		if err != nil {
			return err
		}
		return agentchannels.UpdateAgentSlackKeys(globalDB, agentID, kv)
	}
}

// loadAgentSlackAppToken reads the app token pair stored for agentID.
func loadAgentSlackAppToken(agentID string) agentslack.LoadAppTokenFn {
	return func() (string, string, time.Time, error) {
		if globalDB == nil {
			return "", "", time.Time{}, errors.New("db not ready")
		}
		cfg, _, err := agentSlackStore().LoadSlackForAgent(agentID)
		if err != nil {
			return "", "", time.Time{}, err
		}
		exp, _ := time.Parse(time.RFC3339, cfg.AppTokenExpiresAt)
		return cfg.AppToken, cfg.AppRefreshToken, exp, nil
	}
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
	_, found, err := agentchannels.AgentSlackRow(globalDB, p.ID)
	if err != nil || !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent has no Slack connection"})
		return
	}
	// Only the option keys are merged into the row (under its lock): a
	// whole-map write-back could undo a token rotation saved meanwhile.
	m := map[string]string{}
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
	if err := agentchannels.UpdateAgentSlackKeys(globalDB, p.ID, m); err != nil {
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
	ch.SetPersistAppTokenFn(persistAgentSlackAppToken(p.ID))
	ch.SetLoadAppTokenFn(loadAgentSlackAppToken(p.ID))
	agentID := p.ID
	ch.SetPromptsFn(func() []agentslack.SuggestedPrompt {
		cur, err := globalTeam.Get(context.Background(), agentID)
		if err != nil {
			return nil
		}
		return slackPrompts(team.DecodeSuggestedPrompts(cur.SuggestedPrompts))
	})
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
		defer agentchannels.RecoverPanic("slack", key)
		if err := ch.Start(ctx); err != nil {
			log.Warn().Str("instance", key).Err(err).Msg("team: agent slack instance stopped")
		}
	}()
}

// RegisterAgentSlackInstances adds every enabled agent's bot to the
// registry at boot, before the server wires and starts the channels.
func RegisterAgentSlackInstances(ctx context.Context) {
	// Instant agents ride the shared instances; every Slack instance asks
	// this router which agent answers a message.
	agentslack.SetPersonaRouter(instantRouter{})
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
