// Package slack implements the Slack transport for the agents channel
// registry. See package channels for the shared interfaces (Channel,
// SessionChecker, SessionStartHook, …) — this package only adds the
// Slack-specific concrete type.
package slack

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

const (
	// maxSlackChunk is the safe upper bound for a single Slack message.
	// Slack hard limit is 4000 chars; we leave 200 chars headroom for
	// continuation markers.
	maxSlackChunk = 3800

	// reactions used for the agent lifecycle
	reactionQueued  = "hourglass_flowing_sand" // ⏳
	reactionBlocked = "no_entry_sign"          // 🚫
	reactionError   = "x"                      // ❌

	// queueReactionDelay suppresses the ⏳ reaction when the pool dispatches
	// fast enough that the operator wouldn't see it anyway. Only sessions
	// that are still waiting after this delay get the queue indicator.
	queueReactionDelay = 3 * time.Second
)

// turn holds the per-turn state for a Slack session (thread). A new turn
// is created each time the user sends a message to the thread. All fields
// are protected by Channel.mu.
//
// Key invariant: when handleMessage replaces an old turn with a new one,
// it carries over the accumulated text so TextDelta events that arrive
// between the old and new turn boundaries are not dropped.
type turn struct {
	channelID  string
	msgTS      string // ts of the user message — used for reactions
	buf        strings.Builder
	hasStarted bool // true after first TextDelta (banner already set)
	// queueTimer fires after queueReactionDelay if the pool hasn't accepted
	// the message yet. Cancelled (and set to nil) on successful dispatch
	// so a fast-path send never flashes the ⏳ reaction.
	queueTimer *time.Timer
	// queueShown is set when queueTimer actually fired and added ⏳, so
	// downstream cleanup knows to remove it.
	queueShown bool
	// approval tracking
	pendingApprovalID    string // gate request UUID while waiting for decision
	pendingApprovalMsgTS string // ts of the Slack approval message (for update)
}

// Channel implements agentchannels.Channel for Slack, supporting both
// Socket Mode (default — no public URL required) and HTTP Event API
// (requires public URL).
//
// Lifecycle (per incoming message):
//  1. Parse event → extract channel_id, thread_ts, user_id, text
//  2. Access control check (everyone / users / groups)
//  3. Meta-command intercept (dashboard, reset, status, log, agent)
//  4. Dispatch to pool via sendFn
//  5. On agent events: update reactions + post chunked final reply
//
// Hot-reload: call Reload(ctx, newCfg, pubURL) to apply new credentials
// without restarting the server.
type Channel struct {
	sendFn agentchannels.SendFunc

	cfgMu  sync.Mutex
	cfg    agentconfig.SlackChannelConfig
	pubURL string
	api    *slackgo.Client
	socket *socketmode.Client

	mu    sync.Mutex
	turns map[string]*turn

	approveFn      agentchannels.ApproveFn
	sessions       agentchannels.SessionChecker
	onSessionStart agentchannels.SessionStartHook

	runMu     sync.Mutex
	runCancel context.CancelFunc
	runWg     sync.WaitGroup
}

// New builds a Slack Channel from the operator-supplied config alone.
// All other dependencies are wired by *agentchannels.Registry via the
// corresponding Set* setters before Start.
func New(cfg agentconfig.SlackChannelConfig) *Channel {
	ch := &Channel{
		turns: make(map[string]*turn),
	}
	ch.applyConfig(cfg, "")
	return ch
}

// SetSendFunc satisfies channels.SendFuncSetter.
func (s *Channel) SetSendFunc(fn agentchannels.SendFunc) { s.sendFn = fn }

// SetPublicURL satisfies channels.PublicURLSetter.
func (s *Channel) SetPublicURL(u string) {
	s.cfgMu.Lock()
	s.pubURL = u
	s.cfgMu.Unlock()
}

// SetApproveFn satisfies channels.ApproveFnSetter.
func (s *Channel) SetApproveFn(fn agentchannels.ApproveFn) {
	s.mu.Lock()
	s.approveFn = fn
	s.mu.Unlock()
}

// SetSessionChecker satisfies channels.SessionCheckerSetter.
func (s *Channel) SetSessionChecker(c agentchannels.SessionChecker) { s.sessions = c }

// SetSessionStartHook satisfies channels.SessionStartHookSetter.
func (s *Channel) SetSessionStartHook(fn agentchannels.SessionStartHook) {
	s.onSessionStart = fn
}

// HTTPPath returns the mux path for the Slack webhook (channels.HTTPHandlerProvider).
func (s *Channel) HTTPPath() string { return "POST /integrations/slack/events" }

// applyConfig replaces cfg/pubURL/api/socket atomically.
func (s *Channel) applyConfig(cfg agentconfig.SlackChannelConfig, pubURL string) {
	api := slackgo.New(cfg.BotToken, slackgo.OptionAppLevelToken(cfg.AppToken))
	socket := socketmode.New(api)
	s.cfgMu.Lock()
	s.cfg = cfg
	s.pubURL = pubURL
	s.api = api
	s.socket = socket
	s.cfgMu.Unlock()
}

// Name satisfies Channel.
func (s *Channel) Name() string { return "slack" }

// IsConfigured returns true when the config has the minimum required
// fields to start. Required fields by mode:
//   - socket: BotToken + AppToken
//   - http:   BotToken + SigningSecret
func (s *Channel) IsConfigured() bool {
	s.cfgMu.Lock()
	cfg := s.cfg
	s.cfgMu.Unlock()
	if cfg.BotToken == "" {
		return false
	}
	if cfg.Mode == "http" {
		return cfg.SigningSecret != ""
	}
	return cfg.AppToken != ""
}

// Start begins listening for Slack events. Blocks until ctx is cancelled
// or Stop/Reload is called.
func (s *Channel) Start(ctx context.Context) error {
	s.cfgMu.Lock()
	cfg := s.cfg
	socket := s.socket
	s.cfgMu.Unlock()

	if cfg.BotToken == "" {
		return fmt.Errorf("slack: bot token is required")
	}

	runCtx, runCancel := context.WithCancel(ctx)
	s.runMu.Lock()
	s.runCancel = runCancel
	s.runMu.Unlock()

	s.runWg.Add(1)
	defer func() {
		s.runWg.Done()
		runCancel()
	}()

	if cfg.Mode == "http" {
		if cfg.SigningSecret == "" {
			return fmt.Errorf("slack: signing secret is required for http mode")
		}
		log.Info().Str("channel", "slack").Str("mode", "http").
			Msg("started — receiving events on POST /integrations/slack/events")
		<-runCtx.Done()
		return nil
	}

	if cfg.AppToken == "" {
		return fmt.Errorf("slack: app token (xapp-...) is required for socket mode")
	}

	log.Info().Str("channel", "slack").Str("mode", "socket").Msg("starting")

	go func() {
		if err := socket.RunContext(runCtx); err != nil && runCtx.Err() == nil {
			log.Error().Str("channel", "slack").Err(err).Msg("socket run stopped")
		}
	}()

	for {
		select {
		case <-runCtx.Done():
			return nil
		case evt, ok := <-socket.Events:
			if !ok {
				return nil
			}
			s.handleSocketEvent(ctx, evt)
		}
	}
}

// Stop signals the current Start() to exit gracefully.
func (s *Channel) Stop() {
	s.runMu.Lock()
	cancel := s.runCancel
	s.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Reload stops the current connection, applies new credentials, and
// restarts if the new config is valid.
func (s *Channel) Reload(ctx context.Context, cfg agentconfig.SlackChannelConfig, pubURL string) {
	s.Stop()
	s.runWg.Wait()

	s.applyConfig(cfg, pubURL)

	if !s.IsConfigured() {
		log.Info().Str("channel", "slack").Msg("reload: not configured, staying stopped")
		return
	}

	log.Info().Str("channel", "slack").Msg("reload: restarting with new config")
	go func() {
		if err := s.Start(ctx); err != nil {
			log.Error().Str("channel", "slack").Err(err).Msg("slack channel stopped after reload")
		}
	}()
}

func (s *Channel) handleSocketEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		s.socket.Ack(*evt.Request)
		apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		s.handleEventsAPI(ctx, apiEvent)
	case socketmode.EventTypeInteractive:
		s.socket.Ack(*evt.Request)
		cb, ok := evt.Data.(slackgo.InteractionCallback)
		if !ok {
			return
		}
		go s.handleInteraction(ctx, cb)
	case socketmode.EventTypeConnecting:
		log.Debug().Str("channel", "slack").Msg("connecting")
	case socketmode.EventTypeConnected:
		log.Info().Str("channel", "slack").Msg("connected")
	case socketmode.EventTypeConnectionError:
		log.Warn().Str("channel", "slack").Msg("connection error, will retry")
	}
}

func (s *Channel) handleEventsAPI(ctx context.Context, outer slackevents.EventsAPIEvent) {
	switch outer.Type {
	case slackevents.CallbackEvent:
		switch ev := outer.InnerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			if ev.BotID != "" {
				return
			}
			s.handleMessage(ctx, &slackevents.MessageEvent{
				Type:            ev.Type,
				User:            ev.User,
				Text:            stripBotMention(ev.Text),
				TimeStamp:       ev.TimeStamp,
				ThreadTimeStamp: ev.ThreadTimeStamp,
				Channel:         ev.Channel,
				ChannelType:     "channel",
			})
		case *slackevents.MessageEvent:
			if ev.BotID != "" || ev.SubType != "" {
				return
			}
			// only handle DMs without mention requirement
			if ev.ChannelType != "im" && ev.ChannelType != "mpim" {
				return
			}
			s.handleMessage(ctx, ev)
		}
	}
}

// stripBotMention removes the leading <@BOTID> mention Slack prepends to app_mention text.
func stripBotMention(text string) string {
	if !strings.HasPrefix(text, "<@") {
		return text
	}
	if idx := strings.Index(text, ">"); idx != -1 {
		return strings.TrimSpace(text[idx+1:])
	}
	return text
}

// HTTPHandler returns the webhook handler. Verifies HMAC-SHA256 + handles
// url_verification challenge synchronously; everything else dispatched async.
func (s *Channel) HTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		s.cfgMu.Lock()
		signingSecret := s.cfg.SigningSecret
		s.cfgMu.Unlock()

		if signingSecret == "" {
			http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
			return
		}

		if err := verifySlackSignature(r.Header, body, signingSecret); err != nil {
			log.Warn().Str("channel", "slack").Err(err).Msg("webhook: signature invalid")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		apiEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionNoVerifyToken())
		if err != nil {
			http.Error(w, "parse error", http.StatusBadRequest)
			return
		}

		if apiEvent.Type == slackevents.URLVerification {
			var cr struct {
				Challenge string `json:"challenge"`
			}
			if err := json.Unmarshal(body, &cr); err != nil {
				http.Error(w, "parse error", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"challenge": cr.Challenge})
			return
		}

		w.WriteHeader(http.StatusOK)
		go s.handleEventsAPI(context.Background(), apiEvent)
	})
}

// verifySlackSignature validates the HMAC-SHA256 signature Slack attaches
// to every webhook delivery.
func verifySlackSignature(h http.Header, body []byte, signingSecret string) error {
	timestamp := h.Get("X-Slack-Request-Timestamp")
	if timestamp == "" {
		return fmt.Errorf("missing X-Slack-Request-Timestamp header")
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}
	if math.Abs(float64(time.Now().Unix()-ts)) > 300 {
		return fmt.Errorf("timestamp too old")
	}
	sig := h.Get("X-Slack-Signature")
	if sig == "" {
		return fmt.Errorf("missing X-Slack-Signature header")
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

func (s *Channel) snapshot() agentconfig.SlackChannelConfig {
	s.cfgMu.Lock()
	c := s.cfg
	s.cfgMu.Unlock()
	return c
}

func (s *Channel) handleMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	log.Info().Str("channel", "slack").
		Str("slack_channel", ev.Channel).
		Str("channel_type", ev.ChannelType).
		Str("user", ev.User).
		Str("thread_ts", threadTS).
		Str("msg_ts", ev.TimeStamp).
		Int("text_len", len(ev.Text)).
		Msg("incoming message")

	cfg := s.snapshot()
	groupIDs, err := s.resolveUserGroups(ev.User)
	if err != nil {
		log.Warn().Str("channel", "slack").Str("user", ev.User).Err(err).Msg("resolve groups failed; falling back to empty")
	}
	if !s.allowedCfg(cfg, ev.User, groupIDs, ev.Channel) {
		log.Warn().Str("channel", "slack").
			Str("user", ev.User).
			Str("slack_channel", ev.Channel).
			Str("channel_type", ev.ChannelType).
			Strs("groups", groupIDs).
			Msg("access denied, ignoring message")
		s.setReaction(reactionBlocked, ev.Channel, ev.TimeStamp, "")
		return
	}
	log.Info().Str("channel", "slack").
		Str("user", ev.User).
		Str("slack_channel", ev.Channel).
		Str("channel_type", ev.ChannelType).
		Msg("access allowed")

	meta := agentchannels.ParseMeta(ev.Text)
	if meta.IsMeta {
		s.handleMetaCmd(ctx, meta, ev.Channel, threadTS)
		return
	}

	s.mu.Lock()
	old := s.turns[threadTS]
	t := &turn{channelID: ev.Channel, msgTS: ev.TimeStamp}
	if old != nil {
		t.buf.WriteString(old.buf.String())
		t.hasStarted = old.hasStarted
	}
	s.turns[threadTS] = t
	s.mu.Unlock()

	if old != nil {
		if old.queueTimer != nil {
			old.queueTimer.Stop()
		}
		if old.msgTS != "" && old.queueShown {
			s.cfgMu.Lock()
			api := s.api
			s.cfgMu.Unlock()
			_ = api.RemoveReaction(reactionQueued, slackgo.ItemRef{Channel: old.channelID, Timestamp: old.msgTS})
		}
	}

	// Defer the ⏳ reaction: only show it when the pool is genuinely slow
	// to dispatch (>3s). Fast-path turns never flash the queue emoji.
	chID := ev.Channel
	msgTS := ev.TimeStamp
	t.queueTimer = time.AfterFunc(queueReactionDelay, func() {
		s.mu.Lock()
		cur := s.turns[threadTS]
		// Only mark/show if this turn is still current.
		stillCurrent := cur != nil && cur.msgTS == msgTS
		if stillCurrent {
			cur.queueShown = true
		}
		s.mu.Unlock()
		if !stillCurrent {
			return
		}
		s.setReaction(reactionQueued, chID, msgTS, "")
	})

	if !s.sessionOnDisk(threadTS) {
		ctxText := s.buildSessionContext(ev, threadTS)
		if ctxText != "" {
			if err := s.sendFn(context.Background(), threadTS, "main", "slack", "system", ctxText); err != nil {
				log.Warn().Str("channel", "slack").Str("session", threadTS).Err(err).Msg("inject session context failed")
			}
		}
		if hook := s.onSessionStart; hook != nil {
			hook(threadTS, "slack", ctxText)
		}
	}

	if err := s.sendFn(context.Background(), threadTS, "main", "slack", "user", ev.Text); err != nil {
		log.Error().Str("channel", "slack").Str("session", threadTS).Err(err).Msg("pool send failed")
		old := s.cancelQueueTimer(threadTS, ev.Channel, ev.TimeStamp)
		_ = old
		s.setReaction(reactionError, ev.Channel, ev.TimeStamp, "")
		s.postReply(ev.Channel, threadTS, "Agent error: could not queue message. Check the dashboard for details.")
		return
	}
	// Message accepted by the pool: cancel pending queue timer (and remove
	// ⏳ if it had already fired), then surface the "thinking" banner so
	// the operator sees a working signal even while the agent is still
	// thinking. No "running" emoji — agent status lives in Wick's web UI
	// and the assistant thread banner.
	s.cancelQueueTimer(threadTS, ev.Channel, ev.TimeStamp)
	s.setAssistantStatus(ev.Channel, threadTS, "is thinking…")
}

// cancelQueueTimer stops the pending queue-reaction timer for the named
// turn. If the timer already fired and the ⏳ reaction was added, it is
// removed too. Returns true when the reaction was visible at call time.
func (s *Channel) cancelQueueTimer(threadTS, channelID, msgTS string) bool {
	s.mu.Lock()
	t := s.turns[threadTS]
	var wasShown bool
	if t != nil {
		if t.queueTimer != nil {
			t.queueTimer.Stop()
			t.queueTimer = nil
		}
		wasShown = t.queueShown
		t.queueShown = false
	}
	s.mu.Unlock()
	if !wasShown {
		return false
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return wasShown
	}
	_ = api.RemoveReaction(reactionQueued, slackgo.ItemRef{Channel: channelID, Timestamp: msgTS})
	return wasShown
}

// NotifyState updates reaction + posts reply for the latest turn of sessionKey.
func (s *Channel) NotifyState(sessionKey, state, text string) {
	s.mu.Lock()
	t := s.turns[sessionKey]
	var channelID, msgTS string
	if t != nil {
		channelID = t.channelID
		msgTS = t.msgTS
	}
	s.mu.Unlock()
	if channelID == "" {
		return
	}

	switch state {
	case "running":
		// Reaction already cleared at dispatch; just refresh the banner.
		s.setAssistantStatus(channelID, sessionKey, "is thinking…")
	case "done":
		s.setAssistantStatus(channelID, sessionKey, "")
		if text != "" {
			s.postChunked(channelID, sessionKey, text)
		}
	case "blocked":
		s.setReaction(reactionBlocked, channelID, msgTS, "")
		s.setAssistantStatus(channelID, sessionKey, "")
		note := text
		if note == "" {
			note = "Agent turn completed with blocked commands. See the dashboard for details."
		} else {
			note += "\n\n_Some commands were blocked — see the dashboard for details._"
		}
		s.postChunked(channelID, sessionKey, note)
	case "error":
		s.setReaction(reactionError, channelID, msgTS, "")
		s.setAssistantStatus(channelID, sessionKey, "")
		msg := "Agent error."
		if text != "" {
			msg = fmt.Sprintf("Agent error: %s", text)
		}
		s.postReply(channelID, sessionKey, msg+"\n\nSee the dashboard for details.")
	}
}

// setAssistantStatus updates the Slack AI thread status banner via
// assistant.threads.setStatus. status="" clears the banner. Errors are
// logged at debug only — the workspace may not have Slack AI enabled
// or the bot may lack the assistant:write/chat:write scope; reaction
// emojis remain as the primary progress signal.
func (s *Channel) setAssistantStatus(channelID, threadTS, status string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || channelID == "" || threadTS == "" {
		return
	}
	err := api.SetAssistantThreadsStatus(slackgo.AssistantThreadsSetStatusParameters{
		ChannelID: channelID,
		ThreadTS:  threadTS,
		Status:    status,
	})
	if err != nil {
		log.Info().Str("channel", "slack").Str("slack_channel", channelID).Str("thread_ts", threadTS).Str("status", status).Err(err).Msg("assistant.threads.setStatus failed")
	}
}

// OnAgentEvent satisfies channels.AgentEventReceiver.
func (s *Channel) OnAgentEvent(sessionKey string, ev event.AgentEvent) {
	switch ev.Type {
	case event.TextDelta:
		var notifyRunning bool
		s.mu.Lock()
		t := s.turns[sessionKey]
		if t == nil {
			s.mu.Unlock()
			return
		}
		t.buf.WriteString(ev.Text)
		if !t.hasStarted {
			t.hasStarted = true
			notifyRunning = true
		}
		s.mu.Unlock()
		if notifyRunning {
			s.NotifyState(sessionKey, "running", "")
		}

	case event.Done:
		var text string
		hasError := ev.ErrorMsg != ""
		s.mu.Lock()
		t := s.turns[sessionKey]
		if t == nil {
			s.mu.Unlock()
			return
		}
		text = t.buf.String()
		t.buf.Reset()
		t.hasStarted = false
		s.mu.Unlock()

		state := "done"
		if hasError {
			state = "error"
			text = ev.ErrorMsg
		}
		s.NotifyState(sessionKey, state, text)

	case event.Error:
		s.mu.Lock()
		_, hasTurn := s.turns[sessionKey]
		s.mu.Unlock()
		if !hasTurn {
			return
		}
		msg := ev.ErrorMsg
		if msg == "" {
			msg = ev.Text
		}
		s.NotifyState(sessionKey, "error", msg)
	}
}

func (s *Channel) sessionOnDisk(sessionID string) bool {
	if s.sessions == nil {
		return false
	}
	return s.sessions.SessionExists(sessionID)
}

func (s *Channel) buildSessionContext(ev *slackevents.MessageEvent, threadTS string) string {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return ""
	}

	channelName := ev.Channel
	channelType := ""
	if info, err := api.GetConversationInfo(&slackgo.GetConversationInfoInput{ChannelID: ev.Channel}); err == nil && info != nil {
		if info.Name != "" {
			channelName = "#" + info.Name
		}
		switch {
		case info.IsIM:
			channelType = "direct message"
		case info.IsMpIM:
			channelType = "group DM"
		case info.IsPrivate:
			channelType = "private channel"
		default:
			channelType = "channel"
		}
	}

	userHandle := ev.User
	userReal := ""
	teamName := ""
	if u, err := api.GetUserInfo(ev.User); err == nil && u != nil {
		if u.Name != "" {
			userHandle = "@" + u.Name
		}
		userReal = u.RealName
	}
	if team, err := api.GetTeamInfo(); err == nil && team != nil {
		teamName = team.Name
	}

	permalink := ""
	if pl, err := api.GetPermalink(&slackgo.PermalinkParameters{Channel: ev.Channel, Ts: threadTS}); err == nil {
		permalink = pl
	}

	channelLine := channelName
	if channelType != "" {
		channelLine = fmt.Sprintf("%s (%s)", channelName, channelType)
	}
	userLine := userHandle
	if userReal != "" && userReal != strings.TrimPrefix(userHandle, "@") {
		userLine = fmt.Sprintf("%s (%s)", userHandle, userReal)
	}

	lines := []string{"[Slack thread context — sent automatically by wick]"}
	if teamName != "" {
		lines = append(lines, "Workspace: "+teamName)
	}
	lines = append(lines,
		fmt.Sprintf("Channel: %s [%s]", channelLine, ev.Channel),
		fmt.Sprintf("User: %s [%s]", userLine, ev.User),
		"Thread: "+threadTS,
	)
	if permalink != "" {
		lines = append(lines, "Link: "+permalink)
	}
	return strings.Join(lines, "\n")
}

// OnApprovalRequest satisfies channels.ApprovalReceiver.
func (s *Channel) OnApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	s.mu.Lock()
	t := s.turns[sessionID]
	if t == nil {
		s.mu.Unlock()
		return
	}
	t.pendingApprovalID = req.ID
	channelID := t.channelID
	threadTS := sessionID
	s.mu.Unlock()

	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}

	val := func(decision string) string {
		return decision + "|" + req.ID + "|" + sessionID + "|" + req.MatchKey
	}

	cmd := req.Cmd
	if len(cmd) > 200 {
		cmd = cmd[:200] + "…"
	}

	blocks := []slackgo.Block{
		slackgo.NewHeaderBlock(slackgo.NewTextBlockObject("plain_text", "⚠️ Command Approval Required", false, false)),
		slackgo.NewSectionBlock(
			slackgo.NewTextBlockObject("mrkdwn",
				fmt.Sprintf("*Tool:* `%s`\n*Command:* `%s`\n*Directory:* `%s`",
					req.Tool, cmd, req.WorkDir), false, false),
			nil, nil),
		slackgo.NewActionBlock("gate_approval",
			slackgo.NewButtonBlockElement("gate_approve_once", val(gate.DecisionApproveOnce),
				slackgo.NewTextBlockObject("plain_text", "Allow Once", false, false)),
			slackgo.NewButtonBlockElement("gate_approve_session", val(gate.DecisionApproveSession),
				slackgo.NewTextBlockObject("plain_text", "Allow Session", false, false)),
			slackgo.NewButtonBlockElement("gate_approve_all", val(gate.DecisionApproveAll),
				slackgo.NewTextBlockObject("plain_text", "Allow All (Session)", false, false)).WithStyle(slackgo.StylePrimary),
			slackgo.NewButtonBlockElement("gate_block", val(gate.DecisionBlock),
				slackgo.NewTextBlockObject("plain_text", "Block", false, false)).WithStyle(slackgo.StyleDanger),
		),
	}

	_, approvalTS, err := api.PostMessage(channelID,
		slackgo.MsgOptionBlocks(blocks...),
		slackgo.MsgOptionTS(threadTS),
	)
	if err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("post approval blocks failed")
		return
	}
	s.mu.Lock()
	if t2 := s.turns[sessionID]; t2 != nil {
		t2.pendingApprovalMsgTS = approvalTS
	}
	s.mu.Unlock()
}

// OnApprovalResolved satisfies channels.ApprovalReceiver. Decision /
// expiry / revoke all funnel here — in every case we delete the prompt
// so the thread stays clean (no permanent "Approved" / "Blocked" /
// "Expired" residue).
func (s *Channel) OnApprovalResolved(sessionID, requestID, decision string) {
	_ = decision
	s.mu.Lock()
	t := s.turns[sessionID]
	if t == nil || t.pendingApprovalID != requestID {
		s.mu.Unlock()
		return
	}
	channelID := t.channelID
	approvalMsgTS := t.pendingApprovalMsgTS
	t.pendingApprovalID = ""
	t.pendingApprovalMsgTS = ""
	s.mu.Unlock()

	if approvalMsgTS == "" {
		return
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}

	_, _, err := api.DeleteMessage(channelID, approvalMsgTS)
	if err != nil {
		log.Debug().Str("channel", "slack").Err(err).Msg("delete approval message failed")
	}
}

func (s *Channel) handleInteraction(_ context.Context, cb slackgo.InteractionCallback) {
	if len(cb.ActionCallback.BlockActions) == 0 {
		return
	}
	action := cb.ActionCallback.BlockActions[0]
	if action.BlockID != "gate_approval" {
		return
	}
	parts := strings.SplitN(action.Value, "|", 4)
	if len(parts) != 4 {
		return
	}
	decision, requestID, sessionID, matchKey := parts[0], parts[1], parts[2], parts[3]

	cfg := s.snapshot()
	if !s.approverAllowed(cfg, cb.User.ID) {
		s.cfgMu.Lock()
		api := s.api
		s.cfgMu.Unlock()
		if api != nil {
			_, err := api.PostEphemeral(cb.Channel.ID, cb.User.ID,
				slackgo.MsgOptionText("Not authorized to approve this action.", false),
				slackgo.MsgOptionTS(sessionID),
			)
			if err != nil {
				log.Debug().Str("channel", "slack").Err(err).Msg("post unauthorized ephemeral failed")
			}
		}
		log.Info().Str("channel", "slack").Str("user", cb.User.ID).Msg("approval denied: not in approver set")
		return
	}

	s.mu.Lock()
	fn := s.approveFn
	s.mu.Unlock()
	if fn == nil {
		return
	}
	if err := fn(sessionID, requestID, decision, matchKey); err != nil {
		log.Warn().Str("channel", "slack").Err(err).Msg("gate approval resolve failed")
	}
}

// approverAllowed decides whether userID may resolve a gate button.
//   - trigger_users (default): anyone who passes the normal access checks
//     (users + groups whitelists); channel check is skipped because the
//     approver may be clicking from a different surface than the trigger.
//   - admins: workspace admins / owners (via users.info).
//   - custom: GateApproverUsers list OR a user group in GateApproverGroups.
func (s *Channel) approverAllowed(cfg agentconfig.SlackChannelConfig, userID string) bool {
	switch cfg.GateApprovers {
	case "trigger_users", "":
		groupIDs, _ := s.resolveUserGroups(userID)
		return s.allowedCfg(cfg, userID, groupIDs, "")
	case "admins":
		s.cfgMu.Lock()
		api := s.api
		s.cfgMu.Unlock()
		if api == nil {
			return false
		}
		info, err := api.GetUserInfo(userID)
		if err != nil {
			log.Debug().Str("channel", "slack").Err(err).Msg("users.info failed during approver check")
			return false
		}
		return info.IsAdmin || info.IsOwner || info.IsPrimaryOwner
	case "custom":
		if pickerHas(cfg.GateApproverUsers, userID) {
			return true
		}
		groupIDs, _ := s.resolveUserGroups(userID)
		for _, g := range groupIDs {
			if pickerHas(cfg.GateApproverGroups, g) {
				return true
			}
		}
		return false
	}
	return false
}

func (s *Channel) setReaction(newReaction, channelID, msgTS, oldReaction string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	ref := slackgo.ItemRef{Channel: channelID, Timestamp: msgTS}
	if oldReaction != "" {
		s.withBackoff(func() error {
			return api.RemoveReaction(oldReaction, ref)
		})
	}
	if newReaction == "" {
		return
	}
	s.withBackoff(func() error {
		return api.AddReaction(newReaction, ref)
	})
}

func (s *Channel) postReply(channelID, threadTS, text string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	s.withBackoff(func() error {
		_, _, err := api.PostMessage(
			channelID,
			slackgo.MsgOptionText(text, false),
			slackgo.MsgOptionTS(threadTS),
		)
		return err
	})
}

func (s *Channel) postChunked(channelID, threadTS, text string) {
	chunks := chunkText(text, maxSlackChunk)
	for i, chunk := range chunks {
		msg := chunk
		if i > 0 {
			msg = "_(cont.)_\n" + chunk
		}
		s.postReply(channelID, threadTS, msg)
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

func (s *Channel) withBackoff(fn func() error) {
	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := fn()
		if err == nil {
			return
		}
		if !isRateLimit(err) {
			log.Warn().Str("channel", "slack").Err(err).Msg("slack api call failed")
			return
		}
		wait := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		if wait > 32*time.Second {
			wait = 32 * time.Second
		}
		log.Warn().Str("channel", "slack").Dur("wait", wait).Msg("rate limited, backing off")
		time.Sleep(wait)
	}
	log.Error().Str("channel", "slack").Msg("slack api call failed after max retries")
}

func isRateLimit(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rate_limited") ||
		strings.Contains(err.Error(), "ratelimited")
}

// allowedCfg ANDs three per-list whitelist checks. Modes other than
// "whitelist" (default "all") short-circuit that list to pass.
// channelID may be empty for callers that don't have a channel context
// (e.g. approver checks); the channels list is then skipped.
// allowedCfg combines per-list checks. Semantics:
//   - if BOTH users and groups whitelists are active → OR (match either)
//   - if only ONE is active → it gates alone
//   - if NEITHER is active → pass
//   - channels whitelist is AND'd on top (independent dimension)
func (s *Channel) allowedCfg(cfg agentconfig.SlackChannelConfig, userID string, groupIDs []string, channelID string) bool {
	usersActive := cfg.UsersMode == "whitelist"
	groupsActive := cfg.GroupsMode == "whitelist"

	identityOK := true
	switch {
	case usersActive && groupsActive:
		userMatch := pickerHas(cfg.AllowedUsers, userID)
		groupMatch := false
		for _, gid := range groupIDs {
			if pickerHas(cfg.AllowedGroups, gid) {
				groupMatch = true
				break
			}
		}
		identityOK = userMatch || groupMatch
	case usersActive:
		identityOK = pickerHas(cfg.AllowedUsers, userID)
	case groupsActive:
		identityOK = false
		for _, gid := range groupIDs {
			if pickerHas(cfg.AllowedGroups, gid) {
				identityOK = true
				break
			}
		}
	}

	if !identityOK {
		log.Debug().Str("channel", "slack").Str("reject", "identity").
			Str("user", userID).Strs("groups", groupIDs).
			Bool("users_active", usersActive).Bool("groups_active", groupsActive).
			Msg("denied by identity whitelists")
		return false
	}
	if channelID != "" && cfg.ChannelsMode == "whitelist" && !pickerHas(cfg.AllowedChannels, channelID) {
		log.Debug().Str("channel", "slack").Str("reject", "channels").Str("slack_channel", channelID).Msg("denied by channels whitelist")
		return false
	}
	return true
}

// pickerHas reports whether jsonList (a JSON array of {id,name} entries
// as written by the picker widget) contains id. Empty / malformed lists
// return false. A bare-string list (legacy kvlist) is also accepted by
// falling back to substring scan over the raw bytes.
func pickerHas(jsonList, id string) bool {
	if jsonList == "" || id == "" {
		return false
	}
	var rows []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(jsonList), &rows); err == nil {
		for _, r := range rows {
			if r.ID == id {
				return true
			}
		}
		return false
	}
	for _, entry := range strings.FieldsFunc(jsonList, func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		if strings.TrimSpace(entry) == id {
			return true
		}
	}
	return false
}

func (s *Channel) resolveUserGroups(userID string) ([]string, error) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	groups, err := api.GetUserGroups(slackgo.GetUserGroupsOptionIncludeUsers(true))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, g := range groups {
		for _, uid := range g.Users {
			if uid == userID {
				out = append(out, g.ID)
				break
			}
		}
	}
	return out, nil
}

func (s *Channel) handleMetaCmd(_ context.Context, meta agentchannels.MetaResult, channelID, threadTS string) {
	switch meta.Cmd {
	case "dashboard", "link":
		url := s.dashboardURL(threadTS)
		s.postReply(channelID, threadTS, fmt.Sprintf("Dashboard: %s", url))
	case "status":
		s.postReply(channelID, threadTS, "_Use the dashboard to view real-time agent status._\n"+s.dashboardURL(threadTS))
	case "reset":
		s.postReply(channelID, threadTS, "_Reset acknowledged. The next message will start a fresh context._")
	case "agent":
		if meta.Arg == "" {
			s.postReply(channelID, threadTS, "_Usage: /agent <name>_")
			return
		}
		s.postReply(channelID, threadTS, fmt.Sprintf("_Switching to agent `%s` is not yet wired. Coming soon._", meta.Arg))
	case "log":
		s.postReply(channelID, threadTS, "_Command log: see the dashboard for full details._\n"+s.dashboardURL(threadTS))
	}
}

func (s *Channel) dashboardURL(threadTS string) string {
	s.cfgMu.Lock()
	pubURL := s.pubURL
	s.cfgMu.Unlock()
	base := strings.TrimRight(pubURL, "/")
	if base == "" {
		return "(dashboard URL not configured — set PublicURL in Settings → Agents)"
	}
	return fmt.Sprintf("%s/tools/agents/sessions/%s", base, threadTS)
}
