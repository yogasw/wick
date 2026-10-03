package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	telegramch "github.com/yogasw/wick/internal/agents/channels/telegram"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Keys of an agent Telegram row beyond TelegramChannelConfig's own.
const (
	agentTelegramKeyBotID   = "bot_id"
	agentTelegramKeyBotUser = "bot_username"
)

// telegramAPIEndpoint points getMe at a stub server in tests; "" = Telegram.
var telegramAPIEndpoint = ""

// telegramTokenRe is BotFather's token shape: the bot id, a colon, a secret.
var telegramTokenRe = regexp.MustCompile(`^\d+:[A-Za-z0-9_-]{20,}$`)

func agentTelegramInstanceKey(agentID string) string {
	return agentchannels.AgentTelegramType + ":" + agentID
}

// agentTelegramSessionPrefix namespaces an agent bot's chat sessions apart
// from the Channels page bots' "tg-…" ones.
func agentTelegramSessionPrefix(agentID string) string { return "tgagent-" + agentID + "-" }

// AgentTelegramStatus is GET /api/team/agents/{id}/telegram. The bot token
// is never part of it, not even masked.
type AgentTelegramStatus struct {
	Connected   bool   `json:"connected"`
	Online      bool   `json:"online"`
	BotID       string `json:"bot_id,omitempty"`
	BotUsername string `json:"bot_username,omitempty"`
	Link        string `json:"link,omitempty"`
	Disabled    bool   `json:"disabled"`
}

func agentTelegramStatusOf(p entity.AgentPersona) (AgentTelegramStatus, error) {
	m, err := agentchannels.AgentTelegramConfig(globalDB, p.ID)
	if err != nil {
		return AgentTelegramStatus{}, err
	}
	st := AgentTelegramStatus{
		Connected:   m["bot_token"] != "",
		BotID:       m[agentTelegramKeyBotID],
		BotUsername: m[agentTelegramKeyBotUser],
		Disabled:    p.Disabled,
	}
	if st.BotUsername != "" {
		st.Link = "https://t.me/" + st.BotUsername
	}
	if globalChannels != nil {
		if ch, ok := globalChannels.ChannelByKey(agentTelegramInstanceKey(p.ID)).(*telegramch.Channel); ok && ch != nil {
			st.Online = ch.IsConfigured()
		}
	}
	return st, nil
}

// telegramBotIdentity runs getMe with token. The error never carries the
// token: a transport error quotes the request URL, which embeds it.
func telegramBotIdentity(token string) (botID, username string, err error) {
	endpoint := telegramAPIEndpoint
	if endpoint == "" {
		endpoint = tgbotapi.APIEndpoint
	}
	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint(token, endpoint)
	if err != nil {
		return "", "", errors.New(strings.ReplaceAll(err.Error(), token, "***"))
	}
	return strconv.FormatInt(bot.Self.ID, 10), bot.Self.UserName, nil
}

// errTelegramBotTaken answers a token whose bot already serves another
// agent or a Channels page bot: Telegram hands each update to one poller.
type errTelegramBotTaken struct{ owner string }

func (e errTelegramBotTaken) Error() string {
	return "this Telegram bot is already connected to " + e.owner + " — create a separate bot with @BotFather for each agent"
}

// checkTelegramBotUnique refuses botID when another agent's connection or
// any running Telegram instance already uses it.
func checkTelegramBotUnique(agentID, botID string) error {
	rows, err := agentchannels.ListAgentTelegram(globalDB)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Name == agentID {
			continue
		}
		m := map[string]string{}
		_ = json.Unmarshal([]byte(r.Config), &m)
		if m[agentTelegramKeyBotID] == botID {
			name := "another agent"
			if other, err := globalTeam.Get(context.Background(), r.Name); err == nil {
				name = "@" + other.Handle
			}
			return errTelegramBotTaken{owner: name}
		}
	}
	if globalChannels != nil {
		own := agentTelegramInstanceKey(agentID)
		for _, ch := range globalChannels.Channels() {
			tc, ok := ch.(*telegramch.Channel)
			if !ok || globalChannels.InstanceKeyOf(ch) == own {
				continue
			}
			if id, _, _ := tc.BotIdentity(); id == botID {
				return errTelegramBotTaken{owner: "a channel on the Channels page"}
			}
		}
	}
	return nil
}

// apiTeamAgentTelegramGet handles GET /api/team/agents/{id}/telegram.
func apiTeamAgentTelegramGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	st, err := agentTelegramStatusOf(p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentTelegramConnect handles PUT /api/team/agents/{id}/telegram:
// checks the token with getMe, refuses a bot another agent uses, stores
// it encrypted and brings the bot online.
func apiTeamAgentTelegramConnect(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	token := strings.TrimSpace(req.BotToken)
	if !telegramTokenRe.MatchString(token) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "paste the bot token from @BotFather (123456:ABC…)"})
		return
	}
	botID, username, err := telegramBotIdentity(token)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "Telegram rejected the bot token: " + err.Error()})
		return
	}
	if err := checkTelegramBotUnique(p.ID, botID); err != nil {
		status := http.StatusInternalServerError
		if errors.As(err, new(errTelegramBotTaken)) {
			status = http.StatusConflict
		}
		c.JSON(status, map[string]string{"error": err.Error()})
		return
	}
	stored := token
	if globalConfigs != nil {
		enc, err := globalConfigs.EncryptSecret(token)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": "encrypt secret: " + err.Error()})
			return
		}
		stored = enc
	}
	m := map[string]string{
		"bot_token": stored, "project_id": p.ProjectID,
		agentTelegramKeyBotID: botID, agentTelegramKeyBotUser: username,
	}
	if err := agentchannels.SaveAgentTelegram(globalDB, p.ID, p.OwnerUserID, m, true); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	syncAgentTelegram(context.Background(), p)
	st, _ := agentTelegramStatusOf(p)
	c.JSON(http.StatusOK, st)
}

// apiTeamAgentTelegramTest handles POST /api/team/agents/{id}/telegram/test:
// getMe with the stored token. Reports, never fails the request on a bad
// token, so the card can show why.
func apiTeamAgentTelegramTest(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	cfg, err := agentTelegramStore().LoadTelegramForAgent(p.ID)
	if err != nil || cfg.BotToken == "" {
		c.JSON(http.StatusNotFound, map[string]string{"error": "the agent has no Telegram connection"})
		return
	}
	_, username, err := telegramBotIdentity(cfg.BotToken)
	if err != nil {
		c.JSON(http.StatusOK, map[string]any{"ok": false, "detail": "Telegram rejected the bot token: " + err.Error()})
		return
	}
	st, _ := agentTelegramStatusOf(p)
	detail := "@" + username + " answers"
	if !st.Online {
		detail += ", but the bot is not polling here"
		if p.Disabled {
			detail += " (the agent is disabled)"
		}
	}
	c.JSON(http.StatusOK, map[string]any{"ok": st.Online, "detail": detail, "bot_username": username})
}

// apiTeamAgentTelegramDisconnect handles DELETE /api/team/agents/{id}/telegram.
func apiTeamAgentTelegramDisconnect(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	removeAgentTelegram(p.ID)
	c.JSON(http.StatusOK, map[string]string{"status": "disconnected"})
}

// removeAgentTelegram takes the agent's bot offline and drops its row.
// Called on disconnect and when the agent is deleted.
func removeAgentTelegram(agentID string) {
	if globalChannels != nil && globalChannels.HasKey(agentTelegramInstanceKey(agentID)) {
		globalChannels.RemoveKeyed(agentTelegramInstanceKey(agentID))
	}
	if globalDB != nil {
		if err := agentchannels.DeleteAgentTelegram(globalDB, agentID); err != nil {
			log.Warn().Err(err).Str("agent", agentID).Msg("team: delete agent telegram connection")
		}
	}
}

func agentTelegramStore() agentchannels.DBStore {
	store := agentchannels.NewDBStore(globalDB)
	store.Configs = globalConfigs
	return store
}

// newAgentTelegramInstance builds (not starts) p's bot and adds it to the
// registry. Its chats run in the agent's project, so as the agent.
func newAgentTelegramInstance(p entity.AgentPersona, store agentchannels.DBStore) *telegramch.Channel {
	cfg, err := store.LoadTelegramForAgent(p.ID)
	if err != nil || cfg.BotToken == "" {
		return nil
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = p.ProjectID
	}
	ch := telegramch.NewWithOwner(cfg, p.OwnerUserID)
	ch.SetSendFunc(globalChannels.SendFuncFor("telegram"))
	ch.SetSessionPrefix(agentTelegramSessionPrefix(p.ID))
	globalChannels.AddKeyed(agentTelegramInstanceKey(p.ID), ch, telegramch.NewConfigSourceForAgent(store, ch, p.ID))
	return ch
}

// syncAgentTelegram brings p's bot in line with its row and its state:
// polling when connected and the agent is enabled, gone otherwise.
func syncAgentTelegram(ctx context.Context, p entity.AgentPersona) {
	if globalChannels == nil || globalDB == nil {
		return
	}
	key := agentTelegramInstanceKey(p.ID)
	row, found, err := agentchannels.AgentTelegramRow(globalDB, p.ID)
	if err != nil || !found || !row.Enabled || p.Disabled {
		if globalChannels.HasKey(key) {
			globalChannels.RemoveKeyed(key)
		}
		return
	}
	store := agentTelegramStore()
	if globalChannels.HasKey(key) {
		if ch, ok := globalChannels.ChannelByKey(key).(*telegramch.Channel); ok {
			if cfg, err := store.LoadTelegramForAgent(p.ID); err == nil {
				ch.Reload(ctx, cfg)
			}
		}
		return
	}
	ch := newAgentTelegramInstance(p, store)
	if ch == nil {
		return
	}
	if channelWirer != nil {
		channelWirer(ch)
	}
	go func() {
		defer agentchannels.RecoverPanic("telegram", key)
		if err := ch.Start(ctx); err != nil {
			log.Warn().Str("instance", key).Err(err).Msg("team: agent telegram instance stopped")
		}
	}()
}

// RegisterAgentTelegramInstances adds every enabled agent's bot to the
// registry at boot, before the server wires and starts the channels.
func RegisterAgentTelegramInstances(ctx context.Context) {
	if globalChannels == nil || globalDB == nil || globalTeam == nil {
		return
	}
	rows, err := agentchannels.ListAgentTelegram(globalDB)
	if err != nil {
		log.Warn().Err(err).Msg("team: list agent telegram connections")
		return
	}
	store := agentTelegramStore()
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		p, err := globalTeam.Get(ctx, r.Name)
		if err != nil || p.Disabled {
			continue
		}
		newAgentTelegramInstance(p, store)
	}
}
