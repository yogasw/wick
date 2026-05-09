package channels

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
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
)

const (
	// maxSlackChunk is the safe upper bound for a single Slack message.
	// Slack hard limit is 4000 chars; we leave 200 chars headroom for
	// continuation markers.
	maxSlackChunk = 3800

	// reactions used for the agent lifecycle
	reactionQueued  = "hourglass_flowing_sand" // ⏳
	reactionRunning = "gear"                   // ⚙️
	reactionDone    = "white_check_mark"       // ✅
	reactionBlocked = "no_entry_sign"          // 🚫
	reactionError   = "x"                      // ❌
)

// slackTurn holds the per-turn state for a Slack session (thread). A new turn
// is created each time the user sends a message to the thread. All fields are
// protected by SlackChannel.mu.
//
// Key invariant: when handleMessage replaces an old turn with a new one, it
// carries over the accumulated text so TextDelta events that arrive between
// the old and new turn boundaries are not dropped.
type slackTurn struct {
	channelID  string
	msgTS      string         // ts of the user message — used for reactions
	buf        strings.Builder
	hasStarted bool           // true after first TextDelta (⚙️ already set)
}

// SlackChannel implements Channel for Slack, supporting both Socket Mode
// (default — no public URL required) and HTTP Event API (requires public URL).
//
// Lifecycle (per incoming message):
//  1. Parse event → extract channel_id, thread_ts, user_id, text
//  2. Access control check (everyone / users / groups)
//  3. Meta-command intercept (dashboard, reset, status, log, agent)
//  4. Dispatch to pool via sendFn
//  5. On agent events: update reactions + post chunked final reply
//
// Hot-reload: call Reload(ctx, newCfg, pubURL) to apply new credentials
// without restarting the server. The current connection is gracefully stopped
// and a new one is started if the new config is valid.
type SlackChannel struct {
	sendFn SendFunc

	// cfg, pubURL, api, socket are replaced atomically by Reload().
	// Protected by cfgMu; read under cfgMu or only from a single goroutine.
	cfgMu  sync.Mutex
	cfg    agentconfig.SlackConfig
	pubURL string
	api    *slack.Client
	socket *socketmode.Client

	mu sync.Mutex
	// turns: sessionKey → current turn. Protected by mu.
	// A turn is created on each inbound message and lives until the session
	// is cleaned up. Only the latest turn per session is retained — if the
	// user sends a new message while the agent is still streaming, the new
	// turn carries over the accumulated text so it is not lost.
	turns map[string]*slackTurn

	// runMu guards runCancel; runWg tracks the active Start() call.
	runMu     sync.Mutex
	runCancel context.CancelFunc
	runWg     sync.WaitGroup
}

// NewSlack builds a SlackChannel from the operator-supplied config.
// sendFn is pool.Send (or a wrapper). pubURL is used for dashboard links.
func NewSlack(cfg agentconfig.SlackConfig, sendFn SendFunc, pubURL string) *SlackChannel {
	ch := &SlackChannel{
		sendFn: sendFn,
		turns:  make(map[string]*slackTurn),
	}
	ch.applyConfig(cfg, pubURL)
	return ch
}

// applyConfig replaces cfg/pubURL/api/socket atomically. Called by NewSlack
// and Reload. Must NOT be called while Start() is reading from s.socket.Events.
func (s *SlackChannel) applyConfig(cfg agentconfig.SlackConfig, pubURL string) {
	api := slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	socket := socketmode.New(api)
	s.cfgMu.Lock()
	s.cfg = cfg
	s.pubURL = pubURL
	s.api = api
	s.socket = socket
	s.cfgMu.Unlock()
}

// Name satisfies Channel.
func (s *SlackChannel) Name() string { return "slack" }

// IsConfigured returns true when the config has the minimum required fields
// to start. Server.go uses this to skip the channel gracefully rather than
// treating a missing token as a fatal boot error.
//
// Required fields by mode:
//   - socket: BotToken + AppToken
//   - http:   BotToken + SigningSecret
func (s *SlackChannel) IsConfigured() bool {
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

// Start begins listening for Slack events. It blocks until ctx is cancelled
// or Stop/Reload is called. Only Socket Mode is implemented; HTTP Event API
// is a future extension (config.Mode == "http").
// Call IsConfigured() before Start() — Start returns an error immediately
// when required tokens are absent.
func (s *SlackChannel) Start(ctx context.Context) error {
	s.cfgMu.Lock()
	cfg := s.cfg
	socket := s.socket
	s.cfgMu.Unlock()

	if cfg.BotToken == "" {
		return fmt.Errorf("slack: bot token is required")
	}

	// Create a per-run child context so Reload() can cancel just this
	// connection without cancelling the entire server context.
	runCtx, runCancel := context.WithCancel(ctx)
	s.runMu.Lock()
	s.runCancel = runCancel
	s.runMu.Unlock()

	s.runWg.Add(1)
	defer func() {
		s.runWg.Done()
		runCancel()
	}()

	// HTTP mode: events arrive via HTTPHandler; Start just holds the context
	// open so Reload() has a goroutine to cancel and wait on.
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

	// RunContext blocks and reconnects internally; it exits when runCtx is done.
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
func (s *SlackChannel) Stop() {
	s.runMu.Lock()
	cancel := s.runCancel
	s.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Reload stops the current connection, applies new credentials, and restarts
// if the new config is valid. Safe to call from any goroutine at any time —
// typically called by the server's config watcher when the operator updates
// BotToken/AppToken in the Settings UI without restarting the server.
func (s *SlackChannel) Reload(ctx context.Context, cfg agentconfig.SlackConfig, pubURL string) {
	// Stop the current run and wait for it to exit cleanly before we
	// replace the socket client (avoids reading from a closed Events channel).
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

// handleSocketEvent dispatches a raw socket-mode event to the right handler.
func (s *SlackChannel) handleSocketEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		s.socket.Ack(*evt.Request)
		apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		s.handleEventsAPI(ctx, apiEvent)
	case socketmode.EventTypeConnecting:
		log.Debug().Str("channel", "slack").Msg("connecting")
	case socketmode.EventTypeConnected:
		log.Info().Str("channel", "slack").Msg("connected")
	case socketmode.EventTypeConnectionError:
		log.Warn().Str("channel", "slack").Msg("connection error, will retry")
	}
}

// handleEventsAPI handles Events API payloads (message events etc.).
func (s *SlackChannel) handleEventsAPI(ctx context.Context, outer slackevents.EventsAPIEvent) {
	switch outer.Type {
	case slackevents.CallbackEvent:
		switch ev := outer.InnerEvent.Data.(type) {
		case *slackevents.MessageEvent:
			// Ignore bot messages to avoid feedback loops.
			if ev.BotID != "" || ev.SubType == "bot_message" {
				return
			}
			s.handleMessage(ctx, ev)
		}
	}
}

// HTTPHandler returns an http.Handler for the Slack Events API webhook endpoint.
// Mount it on the public mux at POST /integrations/slack/events — Slack must be
// able to reach the URL without authentication; the signing secret provides
// integrity verification.
//
// Behaviour:
//   - Verifies the HMAC-SHA256 signature on every request.
//   - Responds to the url_verification challenge synchronously.
//   - All other events are dispatched asynchronously so the HTTP response
//     returns well within Slack's 3-second deadline.
func (s *SlackChannel) HTTPHandler() http.Handler {
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

		// URL verification: Slack sends this once when you first configure the
		// webhook URL. Respond synchronously with the challenge value.
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

		// Respond 200 immediately — Slack retries if it doesn't get a timely
		// response, and agent startup can take longer than 3 s.
		w.WriteHeader(http.StatusOK)
		go s.handleEventsAPI(context.Background(), apiEvent)
	})
}

// verifySlackSignature validates the HMAC-SHA256 signature Slack attaches to
// every webhook delivery. Returns a non-nil error when a required header is
// absent, the timestamp is stale (> 5 min, replay-attack prevention), or the
// computed digest does not match the provided signature.
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

// snapshot returns a consistent copy of the connection-independent config
// fields. Use instead of reading s.cfg directly in hot paths.
func (s *SlackChannel) snapshot() agentconfig.SlackConfig {
	s.cfgMu.Lock()
	c := s.cfg
	s.cfgMu.Unlock()
	return c
}

// handleMessage is the main entry point for an inbound user message.
func (s *SlackChannel) handleMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	// thread_ts is the session key. For top-level messages it equals the
	// message ts; for thread replies it is ev.ThreadTimeStamp.
	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp
	}

	// 1. Access control
	cfg := s.snapshot()
	groupIDs, err := s.resolveUserGroups(ev.User)
	if err != nil {
		log.Warn().Str("channel", "slack").Str("user", ev.User).Err(err).Msg("resolve groups failed; falling back to empty")
	}
	if !s.allowedCfg(cfg, ev.User, groupIDs) {
		log.Debug().Str("channel", "slack").Str("user", ev.User).Msg("access denied, ignoring message")
		return
	}

	// 2. Meta-command intercept
	meta := ParseMeta(ev.Text)
	if meta.IsMeta {
		s.handleMetaCmd(ctx, meta, ev.Channel, threadTS)
		return
	}

	// 3. Atomically install a new turn. If a previous turn exists (agent still
	// streaming for an earlier message), carry over its accumulated text so
	// in-flight TextDelta events are not silently dropped.
	s.mu.Lock()
	old := s.turns[threadTS]
	t := &slackTurn{channelID: ev.Channel, msgTS: ev.TimeStamp}
	if old != nil {
		t.buf.WriteString(old.buf.String())
		t.hasStarted = old.hasStarted
	}
	s.turns[threadTS] = t
	s.mu.Unlock()

	// Clean up the old turn's reaction before adding the new ⏳.
	// We know which reaction is active from old.hasStarted:
	//   false → ⏳ was set but agent hasn't started yet
	//   true  → ⚙️ was set (agent was already streaming)
	if old != nil && old.msgTS != "" {
		s.cfgMu.Lock()
		api := s.api
		s.cfgMu.Unlock()
		oldReaction := reactionQueued
		if old.hasStarted {
			oldReaction = reactionRunning
		}
		_ = api.RemoveReaction(oldReaction, slack.ItemRef{Channel: old.channelID, Timestamp: old.msgTS})
	}

	// 4. Add ⏳ reaction immediately so user sees acknowledgment.
	s.setReaction(reactionQueued, ev.Channel, ev.TimeStamp, "")

	// 5. Dispatch to pool — use context.Background() so the agent
	// subprocess lives past this goroutine's lifetime.
	if err := s.sendFn(context.Background(), threadTS, "main", "slack", "user", ev.Text); err != nil {
		log.Error().Str("channel", "slack").Str("session", threadTS).Err(err).Msg("pool send failed")
		s.setReaction(reactionError, ev.Channel, ev.TimeStamp, reactionQueued)
		s.postReply(ev.Channel, threadTS, "Agent error: could not queue message. Check the dashboard for details.")
	}
}

// NotifyState updates the Slack reaction and posts a reply for the current
// turn of sessionKey. It reads channelID and msgTS from the live turn so
// it always targets the LATEST user message even if the user sent follow-ups
// while the agent was streaming.
//
// state: "running" | "done" | "blocked" | "error"
// text is used for "done", "blocked", and "error" states.
func (s *SlackChannel) NotifyState(sessionKey, state, text string) {
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
		s.setReaction(reactionRunning, channelID, msgTS, reactionQueued)
	case "done":
		s.setReaction(reactionDone, channelID, msgTS, reactionRunning)
		if text != "" {
			s.postChunked(channelID, sessionKey, text)
		}
	case "blocked":
		s.setReaction(reactionBlocked, channelID, msgTS, reactionRunning)
		note := text
		if note == "" {
			note = "Agent turn completed with blocked commands. See the dashboard for details."
		} else {
			note += "\n\n_Some commands were blocked — see the dashboard for details._"
		}
		s.postChunked(channelID, sessionKey, note)
	case "error":
		s.setReaction(reactionError, channelID, msgTS, reactionRunning)
		msg := "Agent error."
		if text != "" {
			msg = fmt.Sprintf("Agent error: %s", text)
		}
		s.postReply(channelID, sessionKey, msg+"\n\nSee the dashboard for details.")
	}
}

// OnAgentEvent is called by the server's OnEvent/OnExit hooks for every
// agent event. It routes to the right Slack action based on event type:
//   - First TextDelta of a turn → replace ⏳ with ⚙️ (once per turn)
//   - TextDelta                 → accumulate into the current turn's buffer
//   - Done                      → post final reply + ✅ (or 🚫 if blocked)
//   - Error                     → post error note + ❌
//
// Sessions that did NOT originate from Slack (no turn entry) are ignored
// silently — the SSE broadcaster handles those.
//
// Race safety: if the user sends a follow-up message while the agent is still
// streaming, handleMessage carries over the accumulated text into the new turn
// and updates msgTS. TextDelta events after the swap write to the new turn's
// buffer. Done always posts to the LATEST msgTS, so the reply lands on the
// most recent user message in the thread.
func (s *SlackChannel) OnAgentEvent(sessionKey string, ev event.AgentEvent) {
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

// setReaction removes `old` (if non-empty) and adds `new` on the given message.
// Both operations use exponential backoff for Slack rate limits.
func (s *SlackChannel) setReaction(newReaction, channelID, msgTS, oldReaction string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	ref := slack.ItemRef{Channel: channelID, Timestamp: msgTS}
	if oldReaction != "" {
		s.withBackoff(func() error {
			return api.RemoveReaction(oldReaction, ref)
		})
	}
	s.withBackoff(func() error {
		return api.AddReaction(newReaction, ref)
	})
}

// postReply sends a single message into the thread.
func (s *SlackChannel) postReply(channelID, threadTS, text string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	s.withBackoff(func() error {
		_, _, err := api.PostMessage(
			channelID,
			slack.MsgOptionText(text, false),
			slack.MsgOptionTS(threadTS),
		)
		return err
	})
}

// postChunked splits text at maxSlackChunk chars and posts each chunk as
// a threaded reply. Chunks after the first are prefixed with "(cont.)" so
// the reader sees continuity.
func (s *SlackChannel) postChunked(channelID, threadTS, text string) {
	chunks := chunkText(text, maxSlackChunk)
	for i, chunk := range chunks {
		msg := chunk
		if i > 0 {
			msg = "_(cont.)_\n" + chunk
		}
		s.postReply(channelID, threadTS, msg)
	}
}

// chunkText splits s into chunks of at most max runes, breaking on newlines
// where possible to avoid cutting mid-word.
func chunkText(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var chunks []string
	for len(s) > max {
		cut := max
		// Try to break on a newline within the last 200 chars of the chunk.
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

// withBackoff calls fn with exponential backoff on Slack rate-limit errors
// (HTTP 429). Retries up to 5 times with a cap of 32 s.
func (s *SlackChannel) withBackoff(fn func() error) {
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

// isRateLimit returns true when err is a Slack rate-limit response.
func isRateLimit(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "rate_limited") ||
		strings.Contains(err.Error(), "ratelimited")
}

func (s *SlackChannel) allowedCfg(cfg agentconfig.SlackConfig, userID string, groupIDs []string) bool {
	switch cfg.AccessMode {
	case "everyone", "":
		return true
	case "users":
		return s.inList(cfg.AllowedUsers, userID)
	case "groups":
		for _, gid := range groupIDs {
			if s.inList(cfg.AllowedGroups, gid) {
				return true
			}
		}
		return false
	}
	return false
}

// inList checks whether id appears in the newline/comma-separated list.
// AllowedUsers and AllowedGroups are stored as kvlist (one entry per line
// in the wick config system).
func (s *SlackChannel) inList(list, id string) bool {
	for _, entry := range strings.FieldsFunc(list, func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		if strings.TrimSpace(entry) == id {
			return true
		}
	}
	return false
}

// resolveUserGroups fetches the Slack User Groups that userID belongs to.
// Returns an empty slice (not an error) when groups cannot be resolved —
// access check falls back gracefully.
func (s *SlackChannel) resolveUserGroups(userID string) ([]string, error) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	groups, err := api.GetUserGroups(slack.GetUserGroupsOptionIncludeUsers(true))
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

// handleMetaCmd processes a wick meta-command and replies in the thread.
func (s *SlackChannel) handleMetaCmd(ctx context.Context, meta MetaResult, channelID, threadTS string) {
	switch meta.Cmd {
	case "dashboard", "link":
		url := s.dashboardURL(threadTS)
		s.postReply(channelID, threadTS, fmt.Sprintf("Dashboard: %s", url))
	case "status":
		s.postReply(channelID, threadTS, "_Use the dashboard to view real-time agent status._\n"+s.dashboardURL(threadTS))
	case "reset":
		// Reset is handled by sending a special wick-internal marker to
		// the pool. For now, acknowledge and note it's not yet wired.
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

// dashboardURL builds the session detail URL from PublicURL + thread_ts.
func (s *SlackChannel) dashboardURL(threadTS string) string {
	s.cfgMu.Lock()
	pubURL := s.pubURL
	s.cfgMu.Unlock()
	base := strings.TrimRight(pubURL, "/")
	if base == "" {
		return "(dashboard URL not configured — set PublicURL in Settings → Agents)"
	}
	return fmt.Sprintf("%s/tools/agents/sessions/%s", base, threadTS)
}
