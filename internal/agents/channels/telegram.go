// Package channels — Telegram channel implementation.
//
// Purpose:    Bridges Telegram chats to the wick agent pool via long polling.
//             Handles incoming messages (dispatched to pool via sendFn) and
//             gate approval requests (inline keyboard buttons).
// Caller:     server.go wires TelegramChannel the same way as SlackChannel.
// Dependencies:
//   - github.com/go-telegram-bot-api/telegram-bot-api/v5
//   - internal/agents/gate  (ApprovalRequest, Decision* constants)
//
// Main Functions:
//   - NewTelegram         — construct and validate bot token
//   - Start               — long-poll loop (blocks until ctx cancelled)
//   - Stop                — cancel the poll loop
//   - OnApprovalRequest   — post inline-keyboard approval message
//   - OnApprovalResolved  — edit approval message to show outcome
//   - OnAgentEvent        — stream agent reply back to chat
//   - SetApproveFn        — wire gate resolution callback
//
// Side Effects: Opens a persistent long-poll connection to Telegram servers.

package channels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

// TelegramChannelConfig is the canonical config type for the Telegram channel.
// Defined in internal/agents/config; aliased here for convenience.
type TelegramChannelConfig = agentconfig.TelegramChannelConfig

// pendingApproval tracks an in-flight gate approval message for one session.
type pendingApproval struct {
	chatID    int64
	messageID int
	requestID string
	token     string // short token used in button callback data
}

// telegramCallbackPayload is the server-side lookup entry for a button token.
// Telegram limits callback_data to 64 bytes, so we store the full gate fields
// here and only embed the short token in the button.
type telegramCallbackPayload struct {
	requestID string
	sessionID string
	matchKey  string
}

// telegramTurn holds per-session state for accumulating agent output.
// Telegram has a 4096-char message limit, so we buffer all text and
// send it chunked on Done rather than streaming individual deltas.
type telegramTurn struct {
	chatID int64
	buf    strings.Builder
}

// TelegramChannel implements Channel for Telegram using long polling.
//
// Lifecycle:
//  1. NewTelegram validates the token and builds the BotAPI client.
//  2. Start launches a long-poll loop via tgbotapi.NewUpdate.
//  3. Incoming messages are access-checked then forwarded to sendFn.
//  4. Callback queries prefixed "gate|" are parsed and routed to approveFn.
//  5. OnAgentEvent accumulates text and posts the final reply on Done.
type TelegramChannel struct {
	mu  sync.Mutex
	cfg TelegramChannelConfig
	bot *tgbotapi.BotAPI // nil when not configured

	sendFn    SendFunc
	approveFn ApproveFn

	// pendingApprovals: sessionID → approval message tracking (for editing on resolve)
	pendingApprovals map[string]pendingApproval
	// pendingCallbacks: short token → full gate payload (avoids 64-byte Telegram limit)
	pendingCallbacks map[string]telegramCallbackPayload

	// turns: sessionID → current text accumulation buffer
	turns map[string]*telegramTurn

	runCancel context.CancelFunc
	runWg     sync.WaitGroup
}

// NewTelegram constructs a TelegramChannel. When BotToken is empty or the
// Telegram API rejects it the channel is created in dormant mode (bot == nil)
// so watchTelegramConfig can hot-activate it later without a server restart.
// sendFn is pool.Send (or a wrapper).
func NewTelegram(cfg TelegramChannelConfig, sendFn SendFunc) *TelegramChannel {
	tc := &TelegramChannel{
		cfg:              cfg,
		sendFn:           sendFn,
		pendingApprovals: make(map[string]pendingApproval),
		pendingCallbacks: make(map[string]telegramCallbackPayload),
		turns:            make(map[string]*telegramTurn),
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

// Name satisfies Channel.
func (t *TelegramChannel) Name() string { return "telegram" }

// IsConfigured returns true when a bot token is present.
func (t *TelegramChannel) IsConfigured() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cfg.BotToken != ""
}

// SetApproveFn wires the gate approval resolver. Must be called before Start.
func (t *TelegramChannel) SetApproveFn(fn ApproveFn) {
	t.mu.Lock()
	t.approveFn = fn
	t.mu.Unlock()
}

// Start begins long polling for updates. Blocks until ctx is cancelled or
// Stop is called. Call IsConfigured() before Start — returns an error when
// the bot is nil (unconfigured).
func (t *TelegramChannel) Start(ctx context.Context) error {
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

// Stop signals the current Start() to exit cleanly and waits for it.
func (t *TelegramChannel) Stop() {
	t.mu.Lock()
	cancel := t.runCancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	t.runWg.Wait()
}

// Reload stops the current polling loop, applies the new config, and
// restarts if the new config is valid. Mirrors SlackChannel.Reload so
// operators can update the bot token in Channels → Telegram without
// restarting the server.
func (t *TelegramChannel) Reload(ctx context.Context, cfg agentconfig.TelegramChannelConfig) {
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

// handleUpdate dispatches a single Telegram update to the appropriate handler.
func (t *TelegramChannel) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	switch {
	case update.CallbackQuery != nil:
		t.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		t.handleMessage(ctx, update.Message)
	}
}

// handleMessage processes an inbound text message, applies access control,
// and forwards to the agent pool via sendFn.
func (t *TelegramChannel) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	if msg.Text == "" {
		return
	}

	chatID := msg.Chat.ID
	sessionID := fmt.Sprintf("tg-%d", chatID)

	// Access control: check AllowedIDs if configured.
	t.mu.Lock()
	allowed := t.cfg.AllowedIDs
	sendFn := t.sendFn
	t.mu.Unlock()

	if !t.isChatAllowed(chatID, allowed) {
		log.Debug().Str("channel", "telegram").Int64("chat_id", chatID).Msg("access denied")
		return
	}

	// Ensure a turn exists for this session.
	t.mu.Lock()
	if _, ok := t.turns[sessionID]; !ok {
		t.turns[sessionID] = &telegramTurn{chatID: chatID}
	}
	t.mu.Unlock()

	workspace := t.cfg.Workspace
	if workspace == "" {
		workspace = "main"
	}

	if err := sendFn(ctx, sessionID, workspace, "telegram", "user", msg.Text); err != nil {
		log.Error().Str("channel", "telegram").Str("session", sessionID).Err(err).Msg("pool send failed")
		t.postMessage(chatID, "Agent error: could not queue message. Check the dashboard for details.")
	}
}

// handleCallback processes an inline keyboard callback query. Gate approval
// callbacks are prefixed with "gate|" and contain five pipe-separated fields:
// "gate|decision|requestID|sessionID|matchKey".
func (t *TelegramChannel) handleCallback(_ context.Context, cb *tgbotapi.CallbackQuery) {
	// Always answer the callback to dismiss the loading spinner in Telegram.
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
		return // not a gate approval callback
	}

	// Format: "gate|decision|token" (3 parts).
	// The token is looked up in pendingCallbacks to recover requestID, sessionID,
	// and matchKey — Telegram's 64-byte callback_data limit prevents embedding them.
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

// OnApprovalRequest posts an inline-keyboard message with Allow Once /
// Allow Session / Block buttons. Called by the server when the gate binary
// requests interactive approval for a Telegram-originated session.
func (t *TelegramChannel) OnApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	t.mu.Lock()
	turn := t.turns[sessionID]
	bot := t.bot
	t.mu.Unlock()

	if turn == nil || bot == nil {
		return // not a Telegram-originated session
	}

	chatID := turn.chatID

	// Telegram limits callback_data to 64 bytes. The full gate payload
	// (requestID + sessionID + matchKey) far exceeds this, so we store the
	// payload server-side under a short 8-byte random token and only embed
	// "gate|<decision>|<token>" in the button (≤ 33 bytes for the longest
	// decision string). handleCallback looks up the token to recover the full
	// payload. Keep in sync with handleCallback.
	var tokenBytes [8]byte
	_, _ = rand.Read(tokenBytes[:])
	token := hex.EncodeToString(tokenBytes[:])

	t.mu.Lock()
	t.pendingCallbacks[token] = telegramCallbackPayload{
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

// OnApprovalResolved edits the approval message to replace the inline keyboard
// with the final decision text. Called by the server when any channel (web UI
// or Telegram button) resolves the approval.
func (t *TelegramChannel) OnApprovalResolved(sessionID, requestID, decision string) {
	t.mu.Lock()
	pa, ok := t.pendingApprovals[sessionID]
	if !ok || pa.requestID != requestID {
		t.mu.Unlock()
		return
	}
	delete(t.pendingApprovals, sessionID)
	// Clean up the callback token in case the approval came from the web UI
	// rather than the Telegram button (handleCallback deletes it on button click).
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

// OnAgentEvent accumulates streaming text and posts the final reply when Done.
// Sessions that did not originate from Telegram (no turn entry) are ignored.
func (t *TelegramChannel) OnAgentEvent(sessionID string, ev event.AgentEvent) {
	switch ev.Type {
	case event.TextDelta:
		t.mu.Lock()
		turn := t.turns[sessionID]
		if turn != nil {
			turn.buf.WriteString(ev.Text)
		}
		t.mu.Unlock()

	case event.Done:
		t.mu.Lock()
		turn := t.turns[sessionID]
		if turn == nil {
			t.mu.Unlock()
			return
		}
		text := turn.buf.String()
		chatID := turn.chatID
		turn.buf.Reset()
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
		turn := t.turns[sessionID]
		t.mu.Unlock()
		if turn == nil {
			return
		}
		msg := ev.ErrorMsg
		if msg == "" {
			msg = ev.Text
		}
		t.postMessage(turn.chatID, "Agent error: "+msg)
	}
}

// postMessage sends a single plain-text message to chatID.
func (t *TelegramChannel) postMessage(chatID int64, text string) {
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

// postChunked splits text into Telegram-safe chunks (4096 chars max) and
// sends each as a separate message.
func (t *TelegramChannel) postChunked(chatID int64, text string) {
	const maxTGChunk = 4000
	chunks := chunkText(text, maxTGChunk)
	for _, chunk := range chunks {
		t.postMessage(chatID, chunk)
	}
}

// isChatAllowed checks whether chatID is permitted. If allowedIDs is empty,
// all chats are permitted. Otherwise the ID must appear in the
// newline/comma-separated list.
func (t *TelegramChannel) isChatAllowed(chatID int64, allowedIDs string) bool {
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

// escapeMarkdown escapes special characters for Telegram MarkdownV2 format.
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
