package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	channelsetup "github.com/yogasw/wick/internal/agents/channels/setup"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Taking a Slack app over from the Channels page moves the bot only: the
// Channels instance is switched off (its row, tokens and identity stay),
// and the agent connects the bot under its own key ("slack-agent:<id>")
// and session prefix ("slackagent-<id>-"). Nothing that points at the old
// instance is rewritten — workflow nodes pinned to it and its thread
// sessions stay as they are, so the 409 lists them and the wizard asks
// before going ahead. Turning the instance back on (after disconnecting
// the agent) brings the old instance back — except a rotating app token
// (xoxe.xapp-): the agent refreshes it, which spends the refresh token the
// instance row still holds, so those tokens must be pasted again there.

// codeBotTakenByChannel marks the 409 of a bot a Channels-page instance
// runs, which the wizard answers with "Take over from Channels page".
const codeBotTakenByChannel = "bot_taken_by_channel"

// channelsPageSlackOwner is the user owning a Channels-page Slack instance
// key: "" for "slack:__owner__", the id for "slack:<user-id>". ok is false
// for any other key (agent bots, the shared Instant app).
func channelsPageSlackOwner(key string) (userID string, ok bool) {
	rest, found := strings.CutPrefix(key, "slack:")
	if !found || rest == "" {
		return "", false
	}
	if rest == "__owner__" {
		return "", true
	}
	return rest, true
}

// workflowInstanceRef is one workflow trigger or node pinned to a channel
// instance (its channel_instance field).
type workflowInstanceRef struct {
	WorkflowID   string `json:"workflow_id"`
	WorkflowName string `json:"workflow_name"`
	Kind         string `json:"kind"` // "trigger" | "node"
	ID           string `json:"id"`
	Draft        bool   `json:"draft,omitempty"`
}

// botTakenByChannel is the 409 body of a bot a Channels-page instance
// runs: who holds it and what keeps pointing at that instance after a
// take over.
type botTakenByChannel struct {
	Error         string                `json:"error"`
	Code          string                `json:"code"`
	InstanceKey   string                `json:"instance_key"`
	InstanceName  string                `json:"instance_name"`
	CanTakeOver   bool                  `json:"can_take_over"`
	WorkflowRefs  []workflowInstanceRef `json:"workflow_refs"`
	Sessions      int                   `json:"sessions"`
	SessionPrefix string                `json:"session_prefix"`
	// AccessNote says how the agent's Slack access changes ("Only me" for
	// a first connection); ReplacesBot names the agent's current bot when
	// it already has one; Rotating warns the old instance cannot reuse a
	// rotating app token afterwards.
	AccessNote  string `json:"access_note,omitempty"`
	ReplacesBot string `json:"replaces_bot,omitempty"`
	Rotating    bool   `json:"rotating,omitempty"`
	// InstantAgents ride this instance (Instant mode) and stop answering
	// once it is switched off. Blocker is why the move cannot go ahead
	// (a disabled target agent); can_take_over is false with it.
	InstantAgents []string `json:"instant_agents"`
	Blocker       string   `json:"blocker,omitempty"`
}

// slackTakeover remembers the Channels instances one connect switched off,
// so a failure later in that connect can switch them back on.
type slackTakeover struct {
	restores []func() string
	// users are the Channels rows (by owner, "" = App Owner) switched off.
	users []string
}

// tookOver reports whether the connect switched any instance off.
func (t *slackTakeover) tookOver() bool { return len(t.restores) > 0 }

// undo puts the switched-off instances back as they were, newest first,
// and says what happened to each. Safe to call twice.
func (t *slackTakeover) undo() string {
	var parts []string
	for i := len(t.restores) - 1; i >= 0; i-- {
		parts = append(parts, t.restores[i]())
	}
	t.restores = nil
	return strings.Join(parts, "; ")
}

// carryAppToken hands a rotating app token the agent refreshed during a
// failed take over back to the Channels rows: the pair copied from them
// had its refresh token spent by that refresh, so only the newest pair
// (agent row) still works. copied is the plaintext app token the take over
// started with. Returns a note for the error message.
func (t *slackTakeover) carryAppToken(p entity.AgentPersona, copied string) string {
	if !agentslack.IsRotatingAppToken(copied) || len(t.users) == 0 || globalDB == nil {
		return ""
	}
	// Stop the agent's bot first so it rotates nothing more.
	if globalChannels != nil && globalChannels.HasKey(agentSlackInstanceKey(p.ID)) {
		globalChannels.RemoveKeyed(agentSlackInstanceKey(p.ID))
	}
	warn := " This app uses a rotating app token and its newest pair could not be carried back — paste fresh tokens on the Channels page."
	cfg, _, err := agentSlackStore().LoadSlackForAgent(p.ID)
	if err != nil {
		return warn
	}
	if cfg.AppToken == "" || cfg.AppToken == copied {
		return "" // never refreshed: the Channels row still holds the live pair
	}
	exp, _ := time.Parse(time.RFC3339, cfg.AppTokenExpiresAt)
	kv, err := agentSlackStore().SlackAppTokenKeys(cfg.AppToken, cfg.AppRefreshToken, exp)
	if err != nil {
		return warn
	}
	for _, uid := range t.users {
		if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", uid, kv); err != nil {
			return warn
		}
	}
	return " The agent had already rotated the app token; the newest token pair was carried back to the Channels page."
}

// claimSlackBot makes sure botID is free for agentID, taking it over from
// a Channels-page instance when takeOver is set and the caller is an owner
// or admin. source (Convert) is the caller's own instance the tokens came
// from: it is switched off first, running or not, so it cannot come back
// with the same bot at the next boot. ok=false = the answer was written.
func claimSlackBot(c *tool.Ctx, agentID, botID, botUserID, botName string, takeOver bool, source string) (*slackTakeover, bool) {
	claim := &slackTakeover{}
	u := login.GetUser(c.Context())
	admin := u != nil && u.IsAdmin()
	if source != "" {
		if channelsSlackRunning(source) && !admin {
			c.JSON(http.StatusForbidden, map[string]string{"error": "only an owner or admin can take a running Slack app over from the Channels page — switch the instance off first"})
			return claim, false
		}
		if err := claim.switchOff(c.R.Context(), source); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": "switch off " + source + ": " + err.Error()})
			return claim, false
		}
	}
	fail := func(status int, body any) (*slackTakeover, bool) {
		claim.undo()
		c.JSON(status, body)
		return claim, false
	}
	// Several Channels instances may run the same bot; each is switched
	// off in turn, bounded so a misbehaving registry cannot loop.
	for range 8 {
		err := checkBotUnique(agentID, botID, botUserID)
		if err == nil {
			return claim, true
		}
		var taken errBotTaken
		if !errors.As(err, &taken) {
			return fail(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if taken.instance == "" {
			return fail(http.StatusConflict, map[string]string{"error": err.Error()})
		}
		if !takeOver {
			out := slackTakeoverImpact(taken.instance, botName, err.Error(), agentID, admin)
			out.CanTakeOver = admin && out.Blocker == ""
			return fail(http.StatusConflict, out)
		}
		if !admin {
			return fail(http.StatusForbidden, map[string]string{"error": "only an owner or admin can take a Slack app over from the Channels page"})
		}
		if err := claim.switchOff(c.R.Context(), taken.instance); err != nil {
			return fail(http.StatusInternalServerError, map[string]string{"error": "switch off " + taken.instance + ": " + err.Error()})
		}
		log.Info().Str("instance", taken.instance).Str("agent", agentID).Msg("team: slack app taken over from the Channels page")
	}
	return fail(http.StatusConflict, map[string]string{"error": "the Slack app is still held by a channel on the Channels page"})
}

// channelsSlackRunning reports whether a Channels-page instance key is in
// the registry, i.e. may be serving its bot.
func channelsSlackRunning(key string) bool {
	return globalChannels != nil && globalChannels.HasKey(key)
}

// switchOff flips the Enabled switch of a Channels-page Slack instance off
// and stops it — what the switch on that page does — and remembers how to
// put the switch back.
func (t *slackTakeover) switchOff(ctx context.Context, key string) error {
	userID, ok := channelsPageSlackOwner(key)
	if !ok {
		return errors.New("not a Channels page instance")
	}
	if globalDB == nil {
		return errors.New("db not ready")
	}
	prev, err := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", userID)
	if err != nil {
		return err
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", userID, map[string]string{"enabled": "false"}); err != nil {
		return err
	}
	syncChannelInstance(ctx, "slack", userID)
	// syncChannelInstance needs the DB-backed registry; stop the instance
	// directly too so the bot is free even when it bailed out.
	if globalChannels != nil && globalChannels.HasKey(key) {
		globalChannels.RemoveKeyed(key)
	}
	// A row without the key predates the switch and ran: back to "true".
	was, had := prev["enabled"]
	if !had {
		was = "true"
	}
	t.users = append(t.users, userID)
	t.restores = append(t.restores, func() string {
		if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", userID, map[string]string{"enabled": was}); err != nil {
			log.Warn().Err(err).Str("instance", key).Msg("team: switch the Channels slack instance back on")
			return "the Channels page instance " + key + " could not be switched back on (" + err.Error() + ") — turn it on there"
		}
		syncChannelInstance(context.WithoutCancel(ctx), "slack", userID)
		if was == "false" {
			return "the Channels page instance " + key + " was left off, as it was before"
		}
		return "the Channels page instance " + key + " was switched back on"
	})
	return nil
}

// saveAgentSlack stores an agent's Slack row; a seam for tests.
var saveAgentSlack = agentchannels.SaveAgentSlack

// agentSlackOnlineTimeout bounds how long a take over waits for the
// agent's bot to come online before it rolls back (a var for tests).
var agentSlackOnlineTimeout = 12 * time.Second

// socketStater is the part of a Slack channel the online wait reads.
type socketStater interface {
	SocketState() (string, time.Time)
}

// errTakeoverNeedsSocket: HTTP events are routed to the Channels page
// instances mounted at boot, never to an agent's bot.
const errTakeoverNeedsSocket = "an agent's bot connects over Socket Mode — Slack's HTTP events reach the Channels page instances, not agent bots. Turn Socket Mode on in the Slack app and use its app-level token (xapp-…)"

// waitAgentSlackOnline waits until p's bot is really serving: its Socket
// Mode connection reports "connected" after since (an HTTP-mode bot has
// no connection of its own; it is serving once registered). A var so
// tests can stand in for Slack.
var waitAgentSlackOnline = func(p entity.AgentPersona, mode string, since time.Time) error {
	if globalChannels == nil {
		return nil
	}
	key := agentSlackInstanceKey(p.ID)
	deadline := time.Now().Add(agentSlackOnlineTimeout)
	state := ""
	for {
		ch, _ := globalChannels.ChannelByKey(key).(socketStater)
		if ch == nil {
			return errors.New("the bot was not started")
		}
		if mode == "http" {
			return nil
		}
		var at time.Time
		state, at = ch.SocketState()
		if state == "connected" && !at.Before(since) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("no Socket Mode connection after " + agentSlackOnlineTimeout.String() + ", last state " + firstNonEmpty(state, "none"))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// restoreAgentSlackRow puts p's Slack connection back as it was before a
// failed take over: the previous row (its bot, tokens and on/off) or no
// row at all, and the bot that goes with it.
func restoreAgentSlackRow(p entity.AgentPersona, prev entity.AgentChannel, prevConf map[string]string, had bool) {
	var err error
	if had {
		err = agentchannels.SaveAgentSlack(globalDB, p.ID, p.OwnerUserID, prevConf, prev.Enabled)
	} else {
		err = agentchannels.DeleteAgentSlack(globalDB, p.ID)
	}
	if err != nil {
		log.Warn().Err(err).Str("agent", p.ID).Msg("team: restore slack connection after failed take over")
	}
	syncAgentSlack(context.Background(), p)
}

// slackTakeoverImpact describes taking instance key over for agentID.
// detail=false (a caller who may not take over) leaves out who owns the
// instance and which workflows and sessions use it.
func slackTakeoverImpact(key, botName, msg, agentID string, detail bool) botTakenByChannel {
	userID, _ := channelsPageSlackOwner(key)
	prefix := channelsetup.SessionPrefix("slack", userID)
	out := botTakenByChannel{
		Error:         msg,
		Code:          codeBotTakenByChannel,
		InstanceKey:   key,
		InstanceName:  key,
		WorkflowRefs:  []workflowInstanceRef{},
		SessionPrefix: prefix,
		InstantAgents: []string{},
	}
	if globalTeam != nil {
		if p, err := globalTeam.Get(context.Background(), agentID); err == nil && p.Disabled {
			out.Blocker = "@" + p.Handle + " is disabled — enable it before moving a Slack app to it."
		}
	}
	if !detail {
		return out
	}
	out.InstanceName = channelsPageSlackName(userID, botName)
	if refs := workflowRefsToInstance(key); refs != nil {
		out.WorkflowRefs = refs
	}
	out.Sessions = countInstanceSessions(userID, prefix)
	out.InstantAgents = instantAgentsOn(key)
	if globalDB != nil {
		if row, err := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", userID); err == nil {
			out.Rotating = agentslack.IsRotatingAppToken(decryptedValue(row["app_token"]))
		}
		if m, err := agentchannels.AgentSlackConfig(globalDB, agentID); err == nil {
			if m["bot_token"] != "" {
				out.ReplacesBot = "@" + firstNonEmpty(m[agentSlackKeyBotName], "bot")
			} else if m["users_mode"] == "" {
				out.AccessNote = "Access becomes Only me: only the agent's owner can use the bot until people are added in its Slack settings."
			}
		}
	}
	return out
}

// instantAgentsOn lists the handles of the Instant agents riding Channels
// instance key (an empty shared channel means the App Owner's).
func instantAgentsOn(key string) []string {
	out := []string{}
	if globalDB == nil {
		return out
	}
	rows, err := listInstantRows(globalDB)
	if err != nil {
		return out
	}
	instanceOwner, _ := channelsPageSlackOwner(key)
	for _, r := range rows {
		var cfg instantConfig
		_ = json.Unmarshal([]byte(r.Config), &cfg)
		if !r.Enabled || firstNonEmpty(cfg.SharedChannel, instantOwnerInstance) != key {
			continue
		}
		who := r.Name
		if globalTeam != nil {
			p, err := globalTeam.Get(context.Background(), r.Name)
			if err != nil || p.Disabled {
				continue
			}
			// A user's own instance only carries that user's agents; the
			// App Owner's is shared, so any agent on it counts.
			if instanceOwner != "" && p.OwnerUserID != instanceOwner {
				continue
			}
			who = p.Handle
		}
		out = append(out, "@"+who)
	}
	return out
}

// slackTokenDead are auth.test errors that mean the token itself is
// unusable — retrying cannot help.
var slackTokenDead = map[string]bool{
	"invalid_auth": true, "token_revoked": true, "token_expired": true,
	"account_inactive": true, "not_authed": true, "not_allowed_token_type": true,
}

// decryptStrict is v in plaintext, or an error when v is an encrypted
// value that does not decrypt (decryptedValue would pass the ciphertext on).
func decryptStrict(v string) (string, error) {
	if v == "" || globalConfigs == nil {
		return v, nil
	}
	d, err := globalConfigs.DecryptSecret(v)
	if err == nil {
		return d, nil
	}
	if strings.HasPrefix(v, "wick_") {
		return "", err
	}
	return v, nil
}

// decryptedValue is v in plaintext when it is a stored secret.
func decryptedValue(v string) string {
	if v != "" && globalConfigs != nil {
		if d, err := globalConfigs.DecryptSecret(v); err == nil {
			return d
		}
	}
	return v
}

// channelsPageSlackName labels a Channels-page Slack instance the way the
// page owner would recognise it: "Slack · @bot · <owner>".
func channelsPageSlackName(userID, botName string) string {
	owner := "App Owner"
	if userID != "" {
		owner = userID
		if globalDB != nil {
			var u entity.User
			if err := globalDB.Select("name").Where("id = ?", userID).First(&u).Error; err == nil && u.Name != "" {
				owner = u.Name
			}
		}
	}
	name := "Slack"
	if botName != "" {
		name += " · @" + botName
	}
	return name + " · " + owner
}

// workflowRefsToInstance lists the triggers and nodes of every workflow
// (published body and draft) pinned to instance key.
func workflowRefsToInstance(key string) []workflowInstanceRef {
	if globalWorkflowMgr == nil || globalWorkflowMgr.Repo == nil {
		return nil
	}
	rows, err := globalWorkflowMgr.Repo.List()
	if err != nil {
		return nil
	}
	var out []workflowInstanceRef
	for _, r := range rows {
		out = append(out, refsInWorkflowBody(r.ID, r.Name, r.BodyPublished, false, key)...)
		if r.HasDraft && r.BodyDraft != r.BodyPublished {
			out = append(out, refsInWorkflowBody(r.ID, r.Name, r.BodyDraft, true, key)...)
		}
	}
	return out
}

func refsInWorkflowBody(id, name, body string, draft bool, key string) []workflowInstanceRef {
	if body == "" || !strings.Contains(body, key) {
		return nil
	}
	var wf workflow.Workflow
	if err := json.Unmarshal([]byte(body), &wf); err != nil {
		return nil
	}
	var out []workflowInstanceRef
	for _, t := range wf.Triggers {
		if t.ChannelInstance == key {
			out = append(out, workflowInstanceRef{WorkflowID: id, WorkflowName: name, Kind: "trigger", ID: t.ID, Draft: draft})
		}
	}
	for _, n := range wf.Graph.Nodes {
		if n.ChannelInstance == key {
			out = append(out, workflowInstanceRef{WorkflowID: id, WorkflowName: name, Kind: "node", ID: n.ID, Draft: draft})
		}
	}
	return out
}

// countInstanceSessions counts the sessions carrying the Channels-page
// instance's prefix. The App Owner prefix ("slack-") also starts every
// per-user prefix, so those are left out of its count.
func countInstanceSessions(userID, prefix string) int {
	if globalMgr == nil {
		return 0
	}
	var others []string
	if userID == "" && globalDB != nil {
		owners, _ := agentchannels.ListAllChannelUsers(globalDB, "slack")
		for _, o := range owners {
			others = append(others, channelsetup.SessionPrefix("slack", o))
		}
	}
	n := 0
	for id := range globalMgr.Registry().Sessions() {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		other := false
		for _, op := range others {
			if strings.HasPrefix(id, op) {
				other = true
				break
			}
		}
		if !other {
			n++
		}
	}
	return n
}

// "Convert to agent" (Channels page): the caller's own Channels Slack
// instance becomes one of their Team agents' bot. The stored credentials
// are copied server-side (decrypted here, re-encrypted on the agent row),
// so nothing is pasted again, and the connect goes through the wizard's
// own path with take over on — the same switch-off, the same checks.

// agentSlackConvertReq: confirm=false answers the impact only (option B),
// confirm=true converts.
type agentSlackConvertReq struct {
	Confirm bool `json:"confirm"`
}

// channelsSlackKey is the Channels-page instance key of userID ("" = App
// Owner).
func channelsSlackKey(userID string) string {
	if userID == "" {
		return "slack:__owner__"
	}
	return "slack:" + userID
}

// apiTeamAgentSlackConvert handles POST /api/team/agents/{id}/slack/convert.
func apiTeamAgentSlackConvert(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	// JSON only: a cross-site form cannot send application/json without a
	// preflight.
	if !strings.HasPrefix(c.R.Header.Get("Content-Type"), "application/json") {
		c.JSON(http.StatusUnsupportedMediaType, map[string]string{"error": "send application/json"})
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	if p.Kind != "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "only an agent wick runs itself can take a Slack app"})
		return
	}
	var req agentSlackConvertReq
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<12)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	userID := currentUserIDForChannel(c)
	m, err := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	plain := func(k string) string { return decryptedValue(m[k]) }
	if plain("bot_token") == "" {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no Slack bot is stored on your Channels page"})
		return
	}
	if firstNonEmpty(m["mode"], "socket") != "socket" {
		// An agent's own app connects over Socket Mode; the HTTP events
		// endpoint routes to the Channels instances, not to agent bots.
		c.JSON(http.StatusBadRequest, map[string]string{"error": "this Slack app runs in HTTP mode — an agent's bot connects over Socket Mode. Turn Socket Mode on in the Slack app, paste its app-level token (xapp-…) here, then convert."})
		return
	}
	key := channelsSlackKey(userID)
	if !req.Confirm {
		_, botName, _, _ := agentchannels.NewDBStore(globalDB).LoadBotIdentity("slack", userID)
		u := login.GetUser(c.Context())
		// The same gate the convert applies: a running instance needs an
		// owner or admin, one already switched off is the caller's to move.
		can := (u != nil && u.IsAdmin()) || !channelsSlackRunning(key)
		out := slackTakeoverImpact(key, botName, "", p.ID, true)
		out.CanTakeOver = can && out.Blocker == ""
		c.JSON(http.StatusOK, out)
		return
	}
	connectAgentSlackWith(c, p, agentSlackConnectReq{
		Mode:            m["mode"],
		BotToken:        plain("bot_token"),
		AppToken:        plain("app_token"),
		SigningSecret:   plain("signing_secret"),
		AppRefreshToken: plain("app_refresh_token"),
		ClientID:        m["client_id"],
		ClientSecret:    plain("client_secret"),
		TakeOver:        true,
		sourceInstance:  key,
	})
}

// slackEnableConflict is why a save on userID's Channels Slack row may not
// (re)start its bot: status 0 when it may. Switching the instance on, or
// pasting a bot token into a row that is on, while a Team agent holds that
// same bot would leave two consumers for one Slack app — 409. When that
// cannot be checked (Slack or the DB failing) the save is refused with 503
// rather than risked.
func slackEnableConflict(userID, key, value string) (int, string) {
	if !(key == "enabled" && value == "true") && !(key == "bot_token" && value != "") {
		return 0, ""
	}
	invalid := func(why string) (int, string) {
		return http.StatusUnprocessableEntity, "Slack rejected this bot token (" + why + ") — paste a valid one"
	}
	unverified := func(why string) (int, string) {
		return http.StatusServiceUnavailable, "could not check whether a Team agent uses this Slack app (" + why + ") — try again"
	}
	if globalDB == nil {
		return unverified("db not ready")
	}
	row, err := agentchannels.GetChannelConfigMapForUser(globalDB, "slack", userID)
	if err != nil {
		return unverified(err.Error())
	}
	var token string
	switch {
	case key == "enabled":
		var err error
		if token, err = decryptStrict(row["bot_token"]); err != nil {
			return http.StatusUnprocessableEntity, "the stored bot token cannot be decrypted — paste it again"
		}
	case !agentchannels.ChannelSwitchedOff(row):
		token = value
	}
	if token == "" {
		return 0, ""
	}
	rows, err := agentchannels.ListAgentSlack(globalDB)
	if err != nil {
		return unverified(err.Error())
	}
	if len(rows) == 0 {
		return 0, ""
	}
	botID, _, _, _, err := slackBotIdentity(token)
	switch {
	case err != nil && slackTokenDead[err.Error()]:
		// The token can serve no bot at all, so it cannot clash.
		return invalid(err.Error())
	case err != nil:
		// Network, rate limit, Slack 5xx: worth trying again.
		return unverified("Slack auth.test failed: " + err.Error())
	case botID == "":
		return invalid("not a bot token")
	}
	for _, r := range rows {
		m := map[string]string{}
		_ = json.Unmarshal([]byte(r.Config), &m)
		if m[agentSlackKeyBotID] != botID {
			continue
		}
		who := "a Team agent"
		if globalTeam != nil {
			if p, err := globalTeam.Get(context.Background(), r.Name); err == nil {
				who = "@" + p.Handle
			}
		}
		return http.StatusConflict, "this Slack app is connected to " + who + " in Team — disconnect it there first, then turn this instance back on"
	}
	return 0, ""
}

// convertTargetAgents are the agents userID may convert a Channels Slack
// instance into: their own, run by wick.
func convertTargetAgents(ctx context.Context, userID string) []entity.AgentPersona {
	if globalTeam == nil || userID == "" {
		return nil
	}
	all, err := globalTeam.List(ctx, userID)
	if err != nil {
		return nil
	}
	out := all[:0]
	for _, p := range all {
		if p.Kind == "" && !p.Disabled {
			out = append(out, p)
		}
	}
	return out
}
