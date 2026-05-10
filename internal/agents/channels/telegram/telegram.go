// Package telegram implements the Telegram transport for the agents
// channel registry. See package channels for the shared interfaces.
//
// Side Effects: Opens a persistent long-poll connection to Telegram servers.
package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

// pendingApproval tracks an in-flight gate approval message for one session.
type pendingApproval struct {
	chatID    int64
	messageID int
	requestID string
	token     string
}

// callbackPayload is the server-side lookup entry for a button token.
type callbackPayload struct {
	requestID string
	sessionID string
	matchKey  string
}

// turn holds per-session state for accumulating agent output.
type turn struct {
	chatID int64
	buf    strings.Builder
}

// Channel implements agentchannels.Channel for Telegram via long polling.
type Channel struct {
	mu  sync.Mutex
	cfg agentconfig.TelegramChannelConfig
	bot *tgbotapi.BotAPI

	sendFn         agentchannels.SendFunc
	approveFn      agentchannels.ApproveFn
	sessions       agentchannels.SessionChecker
	onSessionStart agentchannels.SessionStartHook

	pendingApprovals map[string]pendingApproval
	pendingCallbacks map[string]callbackPayload

	turns map[string]*turn

	runCancel context.CancelFunc
	runWg     sync.WaitGroup
}

// New constructs a Telegram Channel from cfg alone. SendFunc and other
// dependencies are wired by *agentchannels.Registry via setter
// interfaces before Start. When BotToken is empty or invalid the channel
// is dormant — Reload can hot-activate it later.
func New(cfg agentconfig.TelegramChannelConfig) *Channel {
	tc := &Channel{
		cfg:              cfg,
		pendingApprovals: make(map[string]pendingApproval),
		pendingCallbacks: make(map[string]callbackPayload),
		turns:            make(map[string]*turn),
	}
	if cfg.BotToken != "" {
		if bot, err := tgbotapi.NewBotAPI(cfg.BotToken); err == nil {
			tc.bot = bot
		} else {
			log.Warn().Err(err).Msg("telegram: invalid bot token, starting in dormant mode")
		}
	}
	return tc
}

// SetSendFunc satisfies channels.SendFuncSetter.
func (t *Channel) SetSendFunc(fn agentchannels.SendFunc) {
	t.mu.Lock()
	t.sendFn = fn
	t.mu.Unlock()
}

// Name satisfies Channel.
func (t *Channel) Name() string { return "telegram" }

// IsConfigured returns true when a bot token is present.
func (t *Channel) IsConfigured() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg.BotToken != ""
}

// SetApproveFn satisfies channels.ApproveFnSetter.
func (t *Channel) SetApproveFn(fn agentchannels.ApproveFn) {
	t.mu.Lock()
	t.approveFn = fn
	t.mu.Unlock()
}

// SetSessionChecker satisfies channels.SessionCheckerSetter.
func (t *Channel) SetSessionChecker(c agentchannels.SessionChecker) {
	t.sessions = c
}

// SetSessionStartHook satisfies channels.SessionStartHookSetter.
func (t *Channel) SetSessionStartHook(fn agentchannels.SessionStartHook) {
	t.onSessionStart = fn
}

func (t *Channel) sessionOnDisk(sessionID string) bool {
	if t.sessions == nil {
		return false
	}
	return t.sessions.SessionExists(sessionID)
}

func (t *Channel) buildSessionContext(msg *tgbotapi.Message, sessionID string) string {
	if msg == nil || msg.Chat == nil {
		return ""
	}
	chatType := msg.Chat.Type
	chatTitle := msg.Chat.Title
	if chatTitle == "" && msg.Chat.UserName != "" {
		chatTitle = "@" + msg.Chat.UserName
	}

	t.mu.Lock()
	bot := t.bot
	t.mu.Unlock()
	botName := ""
	if bot != nil {
		botName = bot.Self.UserName
	}

	chatLine := chatType
	if chatTitle != "" {
		chatLine = fmt.Sprintf("%s — %s", chatType, chatTitle)
	}

	lines := []string{"[Telegram chat context — sent automatically by wick]"}
	if botName != "" {
		lines = append(lines, "Bot: @"+botName)
	}
	lines = append(lines, fmt.Sprintf("Chat: %s [id %d]", chatLine, msg.Chat.ID))

	if msg.From != nil {
		name := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
		if name == "" {
			name = msg.From.UserName
		}
		userLine := name
		if msg.From.UserName != "" {
			userLine = fmt.Sprintf("%s (@%s)", name, msg.From.UserName)
		}
		lines = append(lines, fmt.Sprintf("User: %s [id %d]", userLine, msg.From.ID))
	}

	if chatType == "supergroup" && msg.Chat.UserName != "" {
		lines = append(lines, "Link: https://t.me/"+msg.Chat.UserName)
	}
	lines = append(lines, "Session: "+sessionID)
	return strings.Join(lines, "\n")
}

// Start begins long polling. Blocks until ctx is cancelled or Stop is called.
func (t *Channel) Start(ctx context.Context) error {
	t.mu.Lock()
	bot := t.bot
	t.mu.Unlock()
	if bot == nil {
		return fmt.Errorf("telegram: not configured")
	}

	runCtx, runCancel := context.WithCancel(ctx)
	t.mu.Lock()
	t.runCancel = runCancel
	t.mu.Unlock()

	t.runWg.Add(1)
	defer func() {
		t.runWg.Done()
		runCancel()
	}()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)
	log.Info().Str("channel", "telegram").Str("bot", bot.Self.UserName).Msg("started long polling")

	for {
		select {
		case <-runCtx.Done():
			bot.StopReceivingUpdates()
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			go t.handleUpdate(runCtx, update)
		}
	}
}

// Stop cancels the poll loop and waits for it.
func (t *Channel) Stop() {
	t.mu.Lock()
	cancel := t.runCancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	t.runWg.Wait()
}

// Reload stops, applies new config, and restarts if valid.
func (t *Channel) Reload(ctx context.Context, cfg agentconfig.TelegramChannelConfig) {
	t.Stop()

	if cfg.BotToken == "" {
		t.mu.Lock()
		t.cfg = cfg
		t.bot = nil
		t.mu.Unlock()
		log.Info().Str("channel", "telegram").Msg("reload: no token, staying stopped")
		return
	}

	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		log.Warn().Str("channel", "telegram").Err(err).Msg("reload: invalid bot token, staying stopped")
		return
	}

	t.mu.Lock()
	t.cfg = cfg
	t.bot = bot
	t.mu.Unlock()

	log.Info().Str("channel", "telegram").Str("bot", bot.Self.UserName).Msg("reload: restarting with new config")
	go func() {
		if err := t.Start(ctx); err != nil {
			log.Error().Str("channel", "telegram").Err(err).Msg("telegram channel stopped after reload")
		}
	}()
}

func (t *Channel) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	switch {
	case update.CallbackQuery != nil:
		t.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		t.handleMessage(ctx, update.Message)
	}
}

func (t *Channel) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg.Text == "" {
		return
	}

	chatID := msg.Chat.ID
	sessionID := fmt.Sprintf("tg-%d", chatID)

	t.mu.Lock()
	allowed := t.cfg.AllowedIDs
	sendFn := t.sendFn
	t.mu.Unlock()

	if !t.isChatAllowed(chatID, allowed) {
		log.Debug().Str("channel", "telegram").Int64("chat_id", chatID).Msg("access denied")
		return
	}

	t.mu.Lock()
	if _, ok := t.turns[sessionID]; !ok {
		t.turns[sessionID] = &turn{chatID: chatID}
	}
	t.mu.Unlock()

	workspace := t.cfg.Workspace
	if workspace == "" {
		workspace = "main"
	}

	if !t.sessionOnDisk(sessionID) {
		ctxText := t.buildSessionContext(msg, sessionID)
		if ctxText != "" {
			if err := sendFn(ctx, sessionID, workspace, "telegram", "system", ctxText); err != nil {
				log.Warn().Str("channel", "telegram").Str("session", sessionID).Err(err).Msg("inject session context failed")
			}
		}
		if hook := t.onSessionStart; hook != nil {
			hook(sessionID, "telegram", ctxText)
		}
	}

	if err := sendFn(ctx, sessionID, workspace, "telegram", "user", msg.Text); err != nil {
		log.Error().Str("channel", "telegram").Str("session", sessionID).Err(err).Msg("pool send failed")
		t.postMessage(chatID, "Agent error: could not queue message. Check the dashboard for details.")
	}
}

func (t *Channel) handleCallback(_ context.Context, cb *tgbotapi.CallbackQuery) {
	answer := tgbotapi.NewCallback(cb.ID, "")
	t.mu.Lock()
	bot := t.bot
	fn := t.approveFn
	t.mu.Unlock()

	if bot != nil {
		if _, err := bot.Request(answer); err != nil {
			log.Debug().Str("channel", "telegram").Err(err).Msg("answer callback failed")
		}
	}

	data := cb.Data
	if !strings.HasPrefix(data, "gate|") {
		return
	}

	parts := strings.SplitN(data, "|", 3)
	if len(parts) != 3 {
		log.Warn().Str("channel", "telegram").Str("data", data).Msg("malformed gate callback data")
		return
	}
	_, decision, token := parts[0], parts[1], parts[2]

	t.mu.Lock()
	payload, ok := t.pendingCallbacks[token]
	if ok {
		delete(t.pendingCallbacks, token)
	}
	t.mu.Unlock()

	if !ok {
		log.Warn().Str("channel", "telegram").Str("token", token).Msg("gate callback token not found (already resolved?)")
		return
	}

	if fn == nil {
		log.Warn().Str("channel", "telegram").Msg("approveFn not set; dropping gate callback")
		return
	}
	if err := fn(payload.sessionID, payload.requestID, decision, payload.matchKey); err != nil {
		log.Warn().Str("channel", "telegram").Err(err).Msg("gate approval resolve failed")
	}
}

// OnApprovalRequest satisfies channels.ApprovalReceiver.
func (t *Channel) OnApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	t.mu.Lock()
	tn := t.turns[sessionID]
	bot := t.bot
	t.mu.Unlock()

	if tn == nil || bot == nil {
		return
	}

	chatID := tn.chatID

	var tokenBytes [8]byte
	_, _ = rand.Read(tokenBytes[:])
	token := hex.EncodeToString(tokenBytes[:])

	t.mu.Lock()
	t.pendingCallbacks[token] = callbackPayload{
		requestID: req.ID,
		sessionID: sessionID,
		matchKey:  req.MatchKey,
	}
	t.mu.Unlock()

	btnData := func(decision string) string {
		return "gate|" + decision + "|" + token
	}

	cmd := req.Cmd
	if len(cmd) > 200 {
		cmd = cmd[:200] + "…"
	}

	text := fmt.Sprintf(
		"⚠️ *Command Approval Required*\nTool: `%s`\nCommand: `%s`\nDirectory: `%s`",
		escapeMarkdown(req.Tool),
		escapeMarkdown(cmd),
		escapeMarkdown(req.WorkDir),
	)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Allow Once", btnData(gate.DecisionApproveOnce)),
			tgbotapi.NewInlineKeyboardButtonData("✅ Allow Session", btnData(gate.DecisionApproveSession)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Allow All (Session)", btnData(gate.DecisionApproveAll)),
			tgbotapi.NewInlineKeyboardButtonData("🚫 Block", btnData(gate.DecisionBlock)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	msg.ReplyMarkup = keyboard

	sent, err := bot.Send(msg)
	if err != nil {
		log.Warn().Str("channel", "telegram").Err(err).Msg("post approval message failed")
		return
	}

	t.mu.Lock()
	t.pendingApprovals[sessionID] = pendingApproval{
		chatID:    chatID,
		messageID: sent.MessageID,
		requestID: req.ID,
		token:     token,
	}
	t.mu.Unlock()
}

// OnApprovalResolved satisfies channels.ApprovalReceiver.
func (t *Channel) OnApprovalResolved(sessionID, requestID, decision string) {
	t.mu.Lock()
	pa, ok := t.pendingApprovals[sessionID]
	if !ok || pa.requestID != requestID {
		t.mu.Unlock()
		return
	}
	delete(t.pendingApprovals, sessionID)
	if pa.token != "" {
		delete(t.pendingCallbacks, pa.token)
	}
	bot := t.bot
	t.mu.Unlock()

	if bot == nil {
		return
	}

	label := "✅ Approved"
	switch decision {
	case gate.DecisionBlock:
		label = "🚫 Blocked"
	case gate.DecisionApproveSession:
		label = "✅ Approved for session"
	case gate.DecisionApproveAll:
		label = "✅ All commands allowed for session"
	case gate.DecisionApproveAlways:
		label = "✅ Always allowed"
	}

	edit := tgbotapi.NewEditMessageText(pa.chatID, pa.messageID, label)
	if _, err := bot.Send(edit); err != nil {
		log.Debug().Str("channel", "telegram").Err(err).Msg("edit approval message failed")
	}
}

// OnAgentEvent satisfies channels.AgentEventReceiver.
func (t *Channel) OnAgentEvent(sessionID string, ev event.AgentEvent) {
	switch ev.Type {
	case event.TextDelta:
		t.mu.Lock()
		tn := t.turns[sessionID]
		if tn != nil {
			tn.buf.WriteString(ev.Text)
		}
		t.mu.Unlock()

	case event.Done:
		t.mu.Lock()
		tn := t.turns[sessionID]
		if tn == nil {
			t.mu.Unlock()
			return
		}
		text := tn.buf.String()
		chatID := tn.chatID
		tn.buf.Reset()
		t.mu.Unlock()

		if ev.ErrorMsg != "" {
			t.postMessage(chatID, "Agent error: "+ev.ErrorMsg)
			return
		}
		if text != "" {
			t.postChunked(chatID, text)
		}

	case event.Error:
		t.mu.Lock()
		tn := t.turns[sessionID]
		t.mu.Unlock()
		if tn == nil {
			return
		}
		msg := ev.ErrorMsg
		if msg == "" {
			msg = ev.Text
		}
		t.postMessage(tn.chatID, "Agent error: "+msg)
	}
}

func (t *Channel) postMessage(chatID int64, text string) {
	t.mu.Lock()
	bot := t.bot
	t.mu.Unlock()
	if bot == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Warn().Str("channel", "telegram").Err(err).Msg("send message failed")
	}
}

func (t *Channel) postChunked(chatID int64, text string) {
	const maxTGChunk = 4000
	chunks := chunkText(text, maxTGChunk)
	for _, chunk := range chunks {
		t.postMessage(chatID, chunk)
	}
}

func chunkText(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var chunks []string
	for len(s) > max {
		cut := max
		if idx := strings.LastIndex(s[:cut], "\n"); idx > cut-200 {
			cut = idx + 1
		}
		chunks = append(chunks, strings.TrimRight(s[:cut], "\n"))
		s = s[cut:]
	}
	if s != "" {
		chunks = append(chunks, s)
	}
	return chunks
}

func (t *Channel) isChatAllowed(chatID int64, allowedIDs string) bool {
	if allowedIDs == "" {
		return true
	}
	idStr := fmt.Sprintf("%d", chatID)
	for _, entry := range strings.FieldsFunc(allowedIDs, func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		if strings.TrimSpace(entry) == idStr {
			return true
		}
	}
	return false
}

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]",
		"(", "\\(", ")", "\\)", "~", "\\~", ">", "\\>",
		"#", "\\#", "+", "\\+", "-", "\\-", "=", "\\=",
		"|", "\\|", "{", "\\{", "}", "\\}", ".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(s)
}
