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
	"net/url"
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

	// reactionTrigger is the emoji that toggles a thread's auto-reply mode.
	// Putting 🤖 on a thread's parent (top) message makes every new reply in
	// that thread go to the agent without an @mention; removing it stops.
	// Slack delivers this as the bare emoji name (no colons). Threads are
	// still created only by @mention — the switch never boots a session.
	reactionTrigger = "robot_face" // 🤖

	// queueReactionDelay suppresses the ⏳ reaction when the pool dispatches
	// fast enough that the operator wouldn't see it anyway. Only sessions
	// that are still waiting after this delay get the queue indicator.
	queueReactionDelay = 3 * time.Second

	// statusAnimInterval drives the animated assistant banner. On each tick
	// the banner repaints with a (varied) phrase plus a cycling "." / ".." /
	// "..." suffix, so the operator sees the agent is alive even on a long
	// tool-use turn with no text deltas. It doubles as the keep-alive Slack
	// needs (it drops the status after ~2 minutes with no reply), so it is
	// kept well under that. ~1.5s is slow enough to avoid flicker / rate
	// limits while still feeling live. Adjustable.
	statusAnimInterval = 1500 * time.Millisecond

	// streamEditInterval throttles the live message edit during a turn. Text
	// deltas accumulate in turn.buf; a ticker flushes the buffer with one
	// chat.update per interval (NOT one per delta), so the reply grows like
	// typing without tripping Slack's ~1/sec chat.update rate limit.
	// Adjustable — raise it if the bot runs across many busy channels.
	streamEditInterval = 1500 * time.Millisecond
)

// Footer (status field) state labels — the short word shown in the composer
// footer, with an animated dot suffix appended. Change these to rename the
// footer states. footerState() maps a detailed activity label to one of them.
const (
	footerThinking = "Thinking" // reasoning / streaming text
	footerWorking  = "Working"  // a tool is running
	footerIdle     = "Idle"     // no activity yet
)

// Back-compat aliases used elsewhere as the generic phase labels.
const (
	statusLabelThinking = footerThinking
	statusLabelWorking  = footerWorking
)

const (
	// traceMax caps how many recent activity lines the loading bubble keeps
	// (Slack rotates through them). bubbleLineMax clips each line so a long
	// command stays short and Slack accepts it.
	traceMax      = 5
	bubbleLineMax = 40
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
	threadTS   string // native Slack thread_ts — used for replies/status (NOT the namespaced session key)
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
	// statusTicker re-asserts the loading bubble every statusAnimInterval
	// while the turn is running: it keeps the bubble alive (Slack drops the
	// status after a 2-minute timeout and on any posted reply) and re-sends
	// loading_messages so it never reverts to Slack's defaults. Stopped (and
	// set to nil) on Done/Error.
	statusTicker *time.Ticker
	statusStop   chan struct{}
	// statusLabel is the latest detailed activity, used for the footer state.
	statusLabel string
	// trace is the recent activity history shown (rotating) in the loading
	// bubble — most recent last, capped at traceMax. Each push appends the
	// latest activity line ("Thinking", "Bash: …", "Read: …").
	trace []string
	// dotPhase cycles 0→1→2→3 each animation tick for the footer's animated
	// "." / ".." / "..." suffix.
	dotPhase int
	// liveTS is the ts of the streaming reply message edited in place via
	// chat.update while the turn produces text. Empty until the first flush
	// posts it. Reset on Done so the next turn starts a fresh message.
	liveTS string
	// lastSent is the body text last written to the live message. The Done
	// reconcile compares the final text against it: equal → skip the update
	// entirely (0 API calls); different → one final chat.update.
	lastSent string
	// editTicker flushes turn.buf to the live message every streamEditInterval
	// while text is still streaming. Stopped (and set to nil) on Done/Error.
	editTicker *time.Ticker
	editStop   chan struct{}
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
	sendFn      agentchannels.SendFunc
	ownerFn     func(ctx context.Context, sessionID, userID string)
	ownerUserID string // wick user who owns this channel row; empty = App Owner

	// sessionPrefix namespaces this instance's session keys so multiple
	// Slack bots (per-user owners, possibly across different workspaces)
	// never collide on a shared threadTS. Set by the registry/setup composer
	// to the instance key plus a separator (e.g. "slack:__owner__:" or
	// "slack:<wickUserID>:"). Empty for a lone unkeyed channel.
	//
	// Invariant: the value passed to sendFn and stored as the turns map key
	// is always sessionPrefix+threadTS; the bare threadTS is kept on each
	// turn for Slack API calls (replies, reactions, status). See sessionKey
	// and the turn.threadTS field.
	sessionPrefix string

	cfgMu          sync.Mutex
	cfg            agentconfig.SlackChannelConfig
	pubURL         string
	api            *slackgo.Client
	socket         *socketmode.Client
	botUserID      string           // Slack user ID of the bot itself (U...), resolved via auth.test
	botUserName    string           // Slack handle of the bot (resp.User)
	teamName       string           // Workspace display name (resp.Team)
	teamDomain     string           // Workspace subdomain extracted from resp.URL
	connectorToken ConnectorTokenFn // optional; nil = no user-token DM support
	wickUserIDFn   WickUserIDFn     // optional; resolves Slack user ID → wick user ID

	mu    sync.Mutex
	turns map[string]*turn

	// autoReply is the set of namespaced session keys whose thread has the
	// 🤖 switch on its parent message. While a key is present, channel
	// replies in that thread are dispatched to the agent without requiring
	// an @mention (see handleMessage). Toggled by handleReactionAdded /
	// handleReactionRemoved. In-memory only — switches reset on restart;
	// re-react to re-arm. Guarded by mu.
	autoReply map[string]bool

	// userTokenCache maps Slack user ID → resolved xoxp token.
	// Avoids repeated connectorToken lookups on every send call.
	userTokenMu    sync.RWMutex
	userTokenCache map[string]string

	// userDisplayCache maps Slack user ID → a resolved "Name (@handle, U…)"
	// label, prefixed onto every inbound turn so the agent always knows who
	// spoke (matters in multi-user threads, and for picking the right
	// connector when replying). Cached because resolving needs a users.info
	// API call per user; the directory rarely changes mid-session.
	userDisplayMu    sync.RWMutex
	userDisplayCache map[string]string

	// tokenRefreshFn rebuilds the full userID→token map from connector rows.
	// Wired at startup via SetTokenRefreshFn; triggered on-demand when a
	// lookup misses and on a 5-minute background ticker.
	tokenRefreshFn   func(ctx context.Context) map[string]string
	tokenRefreshedAt time.Time
	tokenRefreshMu   sync.Mutex

	approveFn      agentchannels.ApproveFn
	sessions       agentchannels.SessionChecker
	onSessionStart agentchannels.SessionStartHook

	// workflowEmit fires for every inbound Slack event the operator
	// might wire as a workflow trigger (messages, interactions, slash
	// commands, view submissions, …). nil when no workflow router is
	// attached; channel-only deployments leave it nil with no overhead.
	workflowEmit WorkflowEventSink

	runMu     sync.Mutex
	runCancel context.CancelFunc
	runWg     sync.WaitGroup

	// socketState tracks the current Socket Mode connection lifecycle so
	// the integration test panel can report "subscribed / not subscribed"
	// without re-initiating a connection. Empty when not started or when
	// running in HTTP mode. Updated by handleSocketEvent.
	socketMu    sync.RWMutex
	socketState string // "", "connecting", "connected", "error", "disconnected"
	socketAt    time.Time
}

// New builds a Slack Channel from the operator-supplied config alone.
// All other dependencies are wired by *agentchannels.Registry via the
// corresponding Set* setters before Start.
func New(cfg agentconfig.SlackChannelConfig) *Channel {
	ch := &Channel{
		turns:            make(map[string]*turn),
		userTokenCache:   make(map[string]string),
		userDisplayCache: make(map[string]string),
		autoReply:        make(map[string]bool),
	}
	ch.applyConfig(cfg, "")
	return ch
}

// NewWithOwner creates a Slack Channel tied to a specific wick user owner.
// ownerUserID="" means the App Owner's channel (user_id = NULL row).
func NewWithOwner(cfg agentconfig.SlackChannelConfig, ownerUserID string) *Channel {
	ch := New(cfg)
	ch.ownerUserID = ownerUserID
	return ch
}

// SetSendFunc satisfies channels.SendFuncSetter.
func (s *Channel) SetSendFunc(fn agentchannels.SendFunc) { s.sendFn = fn }

// SetOwnerFn wires a function that stamps a wick user ID on a session.
func (s *Channel) SetOwnerFn(fn func(ctx context.Context, sessionID, userID string)) {
	s.ownerFn = fn
}

// SetSessionPrefix namespaces this instance's session keys. Called by the
// setup composer with the registry instance key + separator so two Slack
// bots never share a session/pool entry on a coincidentally-equal threadTS
// (e.g. the same timestamp produced in two different workspaces). Safe to
// call once before Start.
func (s *Channel) SetSessionPrefix(prefix string) {
	s.cfgMu.Lock()
	s.sessionPrefix = prefix
	s.cfgMu.Unlock()
}

// sessionKey returns the namespaced session key for a Slack thread:
// sessionPrefix + threadTS. This is the value handed to the pool and used
// as the turns map key, guaranteeing per-instance isolation.
func (s *Channel) sessionKey(threadTS string) string {
	s.cfgMu.Lock()
	p := s.sessionPrefix
	s.cfgMu.Unlock()
	return p + threadTS
}

// API returns the live Slack web-API client. Returns nil when the
// channel isn't configured yet — callers must nil-check before use.
// The workflow action subpackage (slack/workflow) uses this to invoke
// chat.postMessage, views.open, reactions.add, etc.
func (s *Channel) API() *slackgo.Client {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.api
}

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

// SetTokenRefreshFn wires a function that rebuilds the full userID→token map.
// Called at startup and triggered on-demand when a lookup misses.
func (s *Channel) SetTokenRefreshFn(fn func(ctx context.Context) map[string]string) {
	s.cfgMu.Lock()
	s.tokenRefreshFn = fn
	s.cfgMu.Unlock()
}

// RefreshTokenMap rebuilds the userID→token map from connector rows.
// Debounced: skips if called within 60s of last refresh.
func (s *Channel) RefreshTokenMap(ctx context.Context) {
	s.tokenRefreshMu.Lock()
	defer s.tokenRefreshMu.Unlock()
	if time.Since(s.tokenRefreshedAt) < 60*time.Second {
		return
	}
	s.cfgMu.Lock()
	fn := s.tokenRefreshFn
	s.cfgMu.Unlock()
	if fn == nil {
		return
	}
	newMap := fn(ctx)
	s.userTokenMu.Lock()
	s.userTokenCache = newMap
	s.userTokenMu.Unlock()
	s.tokenRefreshedAt = time.Now()
	log.Info().Int("users", len(newMap)).Msg("slack: user token map refreshed")
}

// HTTPHandlers satisfies channels.MultiHTTPHandlerProvider.
// Returns two routes:
//   - POST /integrations/slack/events — inbound Slack webhook
//   - POST /integrations/slack/send   — local agent proxy, no external auth
//
// OAuth routes (start/callback) have moved to the generic connector manager
// at /manager/connectors/slack/oauth/* and are no longer registered here.
func (s *Channel) HTTPHandlers() map[string]http.Handler {
	return map[string]http.Handler{
		"POST /integrations/slack/events": s.HTTPHandler(),
		"POST /integrations/slack/send":   s.sendHandler(),
	}
}

// applyConfig replaces cfg/pubURL/api/socket atomically.
// Also resolves the bot's own Slack user ID via auth.test so the footer
// can render as a proper @mention ("Sent using <@UBOT123>").
func (s *Channel) applyConfig(cfg agentconfig.SlackChannelConfig, pubURL string) {
	api := slackgo.New(cfg.BotToken, slackgo.OptionAppLevelToken(cfg.AppToken))
	socket := socketmode.New(api)

	botUserID, botUserName, teamName, teamDomain := "", "", "", ""
	if cfg.BotToken != "" {
		if resp, err := api.AuthTest(); err == nil {
			botUserID = resp.UserID
			teamName = resp.Team
			teamDomain = extractTeamDomain(resp.URL)
			botUserName = resolveBotDisplayName(api, botUserID, resp.User)
		}
	}

	s.cfgMu.Lock()
	s.cfg = cfg
	s.pubURL = pubURL
	s.api = api
	s.socket = socket
	s.botUserID = botUserID
	s.botUserName = botUserName
	s.teamName = teamName
	s.teamDomain = teamDomain
	s.cfgMu.Unlock()
}

// resolveBotDisplayName returns the human-readable name Slack shows in
// the mention picker for this bot. Tries users.info → Profile.RealName
// (the "Display Name" in app settings), then Profile.DisplayName, then
// falls back to the auth.test User slug. Best-effort: any error returns
// the fallback so the status panel still renders something.
func resolveBotDisplayName(api *slackgo.Client, userID, fallback string) string {
	if userID == "" {
		return fallback
	}
	u, err := api.GetUserInfo(userID)
	if err != nil || u == nil {
		return fallback
	}
	if n := strings.TrimSpace(u.Profile.RealName); n != "" {
		return n
	}
	if n := strings.TrimSpace(u.Profile.DisplayName); n != "" {
		return n
	}
	return fallback
}

// extractTeamDomain pulls "acme" out of "https://acme.slack.com/" so
// the status panel can show a clean subdomain instead of the full URL.
func extractTeamDomain(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	rawURL = strings.TrimPrefix(rawURL, "https://")
	rawURL = strings.TrimPrefix(rawURL, "http://")
	if i := strings.Index(rawURL, "."); i > 0 {
		return rawURL[:i]
	}
	return rawURL
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
	s.setSocketState("disconnected")
}

// SocketState returns the current Socket Mode lifecycle state and when
// it was last updated. Empty state means the channel is in HTTP mode
// or has not started yet. Used by the integration test panel to report
// "subscribed / not subscribed" without re-initiating a connection.
func (s *Channel) SocketState() (string, time.Time) {
	s.socketMu.RLock()
	defer s.socketMu.RUnlock()
	return s.socketState, s.socketAt
}

func (s *Channel) setSocketState(state string) {
	s.socketMu.Lock()
	s.socketState = state
	s.socketAt = time.Now()
	s.socketMu.Unlock()
}

// refreshBotUserID re-resolves the bot's Slack user ID via auth.test and
// persists it onto the channel. Called on every successful Socket Mode
// connect so a transient AuthTest failure at applyConfig time gets
// repaired the moment we have a healthy session. No-op on http mode
// (where applyConfig's one-shot resolution is the only attempt).
func (s *Channel) refreshBotUserID(ctx context.Context) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}
	resp, err := api.AuthTestContext(ctx)
	if err != nil {
		log.Warn().Err(err).Str("channel", "slack").Msg("authtest after connect failed")
		return
	}
	displayName := resolveBotDisplayName(api, resp.UserID, resp.User)
	s.cfgMu.Lock()
	prev := s.botUserID
	s.botUserID = resp.UserID
	s.botUserName = displayName
	s.teamName = resp.Team
	s.teamDomain = extractTeamDomain(resp.URL)
	s.cfgMu.Unlock()
	if prev != resp.UserID {
		log.Info().Str("channel", "slack").Str("bot_user_id", resp.UserID).Str("prev", prev).Msg("bot user id refreshed on connect")
	}
}

// BotUserID returns this instance's resolved bot Slack user ID (U...),
// or "" if auth.test hasn't succeeded yet. Exposed so the connector
// "Sent using @bot" footer can name the bot of the instance that OWNS a
// session — regardless of which connector instance does the actual send.
func (s *Channel) BotUserID() string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.botUserID
}

// OwnsSession reports whether sessionID belongs to this instance.
//
// The App Owner instance's prefix is the bare "slack-", which is ALSO a
// prefix of every per-user session ("slack-<uuid>-<ts>"). A plain
// HasPrefix would make the App Owner instance claim per-user sessions and
// stamp the wrong bot. So the App Owner case additionally requires that the
// remainder carry no further "-" segment: a Slack thread_ts is digits + ".",
// never "-", so "slack-<ts>" has none while "slack-<uuid>-<ts>" does.
// Per-user prefixes already end in "-<uuid>-" and are unambiguous.
func (s *Channel) OwnsSession(sessionID string) bool {
	s.cfgMu.Lock()
	p := s.sessionPrefix
	s.cfgMu.Unlock()
	if p == "" || !strings.HasPrefix(sessionID, p) {
		return false
	}
	rest := sessionID[len(p):]
	// App Owner prefix ("<channel>-") owns only sessions whose remainder is a
	// bare transport key with no extra "-" (which would mark a per-user id).
	if strings.Count(p, "-") == 1 {
		return !strings.Contains(rest, "-")
	}
	return true
}

// Status satisfies channels.StatusReporter — returns identity + transport
// state for the admin UI panel under the Test Integration button.
func (s *Channel) Status() []agentchannels.StatusField {
	s.cfgMu.Lock()
	cfg := s.cfg
	botID := s.botUserID
	botName := s.botUserName
	teamName := s.teamName
	teamDomain := s.teamDomain
	pubURL := s.pubURL
	s.cfgMu.Unlock()

	mode := cfg.Mode
	if mode == "" {
		mode = "socket"
	}
	out := []agentchannels.StatusField{
		{Label: "Mode", Value: mode},
	}

	if mode == "socket" {
		state, at := s.SocketState()
		switch state {
		case "connected":
			out = append(out, agentchannels.StatusField{
				Label: "Subscribe",
				Value: fmt.Sprintf("connected (%s ago)", time.Since(at).Round(time.Second)),
				OK:    true,
			})
		case "connecting":
			out = append(out, agentchannels.StatusField{Label: "Subscribe", Value: "connecting…", Warn: true})
		case "error", "disconnected":
			out = append(out, agentchannels.StatusField{Label: "Subscribe", Value: state, Warn: true})
		default:
			out = append(out, agentchannels.StatusField{Label: "Subscribe", Value: "not started", Warn: true})
		}
	} else {
		// http mode
		if pubURL == "" {
			out = append(out, agentchannels.StatusField{Label: "Webhook", Value: "public URL not configured", Warn: true})
		} else {
			out = append(out, agentchannels.StatusField{
				Label: "Webhook",
				Value: pubURL + "/integrations/slack/events",
				OK:    true,
			})
		}
	}

	if botID != "" {
		val := botID
		if botName != "" {
			val = botName + " (" + botID + ")"
		}
		out = append(out, agentchannels.StatusField{Label: "Bot", Value: val, OK: true})
	} else {
		out = append(out, agentchannels.StatusField{Label: "Bot", Value: "unknown — auth.test pending", Warn: true})
	}

	if teamName != "" {
		val := teamName
		if teamDomain != "" {
			val = teamName + " (" + teamDomain + ".slack.com)"
		}
		out = append(out, agentchannels.StatusField{Label: "Workspace", Value: val, OK: true})
	}
	return out
}

// Reconnect re-establishes the Socket Mode connection when the current
// state is not connecting/connected. No-op when already connecting or
// connected (anti-duplicate-subscribe guard). HTTP mode is a no-op.
// Runs Reload in a goroutine so callers (health probe, test panel) do
// not block on the reconnect handshake.
func (s *Channel) Reconnect(ctx context.Context) {
	s.cfgMu.Lock()
	cfg := s.cfg
	pubURL := s.pubURL
	s.cfgMu.Unlock()
	if cfg.Mode == "http" {
		return
	}
	state, _ := s.SocketState()
	if state == "connecting" || state == "connected" {
		return
	}
	go s.Reload(ctx, cfg, pubURL)
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
	case socketmode.EventTypeSlashCommand:
		// Slash commands need an immediate ack so the user doesn't see
		// "operation_timeout" in Slack. Pass-through response goes via
		// the response_url which the workflow can post to via the
		// slack.respond_url action.
		cmd, ok := evt.Data.(slackgo.SlashCommand)
		if !ok {
			s.socket.Ack(*evt.Request)
			return
		}
		s.socket.Ack(*evt.Request)
		go s.handleSlashCommand(ctx, cmd)
	case socketmode.EventTypeConnecting:
		s.setSocketState("connecting")
		log.Debug().Str("channel", "slack").Msg("connecting")
	case socketmode.EventTypeConnected:
		s.setSocketState("connected")
		log.Info().Str("channel", "slack").Msg("connected")
		go s.refreshBotUserID(ctx)
	case socketmode.EventTypeConnectionError:
		s.setSocketState("error")
		log.Warn().Str("channel", "slack").Msg("connection error, will retry")
	}
}

// handleSlashCommand fires the workflow trigger for a slash command.
// The Slack channel itself has no agent-session role for slash
// commands — they exist purely to be workflow-driven.
func (s *Channel) handleSlashCommand(ctx context.Context, cmd slackgo.SlashCommand) {
	s.emitWorkflow(ctx, "command", map[string]any{
		"user":         cmd.UserID,
		"command":      cmd.Command,
		"text":         cmd.Text,
		"channel_id":   cmd.ChannelID,
		"team_id":      cmd.TeamID,
		"trigger_id":   cmd.TriggerID,
		"response_url": cmd.ResponseURL,
	})
}

func (s *Channel) handleEventsAPI(ctx context.Context, outer slackevents.EventsAPIEvent) {
	switch outer.Type {
	case slackevents.CallbackEvent:
		switch ev := outer.InnerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			if ev.BotID != "" {
				return
			}
			cleanText := stripBotMention(ev.Text)
			// Workflow surface first — emit the typed app_mention event
			// before the legacy session dispatch so workflows can route
			// mentions independently of the agent session machinery.
			s.emitWorkflow(ctx, "app_mention", map[string]any{
				"user":       ev.User,
				"text":       cleanText,
				"channel_id": ev.Channel,
				"thread":     threadKey(ev.ThreadTimeStamp, ev.TimeStamp),
				"ts":         ev.TimeStamp,
			})
			// ev.Files carries any images/attachments posted with the mention.
			// MessageEvent has no Files field, so pass them alongside — without
			// this the agent never learns the user attached anything.
			s.handleMessage(ctx, &slackevents.MessageEvent{
				Type:            ev.Type,
				User:            ev.User,
				Text:            cleanText,
				TimeStamp:       ev.TimeStamp,
				ThreadTimeStamp: ev.ThreadTimeStamp,
				Channel:         ev.Channel,
				ChannelType:     "channel",
			}, ev.Files)
		case *slackevents.MessageEvent:
			// SubType "file_share" carries attachments with no real text —
			// let it through (with its files) instead of dropping it. Other
			// subtypes (edits, joins, …) are still ignored.
			if ev.BotID != "" || (ev.SubType != "" && ev.SubType != "file_share") {
				return
			}
			// Workflow surface gets every non-bot message regardless of
			// channel type — operators can scope via channel_id /
			// channel_type match keys. Agent session dispatch below
			// stays DM-only to preserve existing UX.
			s.emitWorkflow(ctx, "message", map[string]any{
				"user":         ev.User,
				"text":         ev.Text,
				"channel_id":   ev.Channel,
				"channel_type": ev.ChannelType,
				"thread":       threadKey(ev.ThreadTimeStamp, ev.TimeStamp),
				"ts":           ev.TimeStamp,
				"is_dm":        ev.ChannelType == "im" || ev.ChannelType == "mpim",
			})
			// Agent session dispatch is DM-only by default. The one
			// exception is a channel thread whose parent carries the 🤖
			// auto-reply switch: there, plain replies (no @mention) are
			// dispatched to the thread's existing session.
			//
			// Mentions in a channel ALSO arrive as a MessageEvent (in addition
			// to the AppMentionEvent that already dispatched them). On an
			// auto-reply thread that MessageEvent would otherwise pass the gate
			// below and dispatch the SAME message a second time — so skip any
			// channel message that mentions the bot; AppMentionEvent owns it.
			if ev.ChannelType != "im" && ev.ChannelType != "mpim" {
				if s.mentionsBot(ev.Text) {
					return
				}
				if !s.autoReplyOn(s.sessionKey(threadKey(ev.ThreadTimeStamp, ev.TimeStamp))) {
					return
				}
			}
			// MessageEvent stashes a file_share's files under .Message
			// (slackevents only populates the top-level Files field on
			// app_mention, not plain messages).
			var files []slackgo.File
			if ev.Message != nil {
				files = ev.Message.Files
			}
			s.handleMessage(ctx, ev, files)
		case *slackevents.AppHomeOpenedEvent:
			s.emitWorkflow(ctx, "app_home_opened", map[string]any{
				"user": ev.User,
				"tab":  ev.Tab,
			})
		case *slackevents.ReactionAddedEvent:
			s.handleReactionAdded(ctx, ev)
		case *slackevents.ReactionRemovedEvent:
			s.handleReactionRemoved(ev)
		}
	}
}

// threadKey returns the conversation thread key: parent thread_ts when
// the message is a reply, otherwise the message's own ts (which is how
// new threads boot — replying to a top-level message uses that ts).
func threadKey(threadTS, msgTS string) string {
	if threadTS != "" {
		return threadTS
	}
	return msgTS
}

// autoReplyOn reports whether the named session's thread has the 🤖
// auto-reply switch on. sessionID is the namespaced session key (which, for
// the app-owner channel, equals the on-disk session ID). The in-memory map
// is a fast cache; on a miss (e.g. right after a restart) it falls back to
// the persisted flag in the session meta so the switch survives restart.
func (s *Channel) autoReplyOn(sessionID string) bool {
	s.mu.Lock()
	on, cached := s.autoReply[sessionID]
	s.mu.Unlock()
	if cached {
		return on
	}
	if s.sessions == nil {
		return false
	}
	on = s.sessions.AutoReplyOn(sessionID)
	if on {
		// Warm the cache so repeated replies don't re-read meta.json.
		s.mu.Lock()
		s.autoReply[sessionID] = true
		s.mu.Unlock()
	}
	return on
}

// setAutoReply flips the auto-reply switch for a session: it updates the
// in-memory cache and persists the flag to the session meta so it survives
// a wick restart. sessionID is the namespaced session key.
func (s *Channel) setAutoReply(sessionID string, on bool) {
	s.mu.Lock()
	if on {
		s.autoReply[sessionID] = true
	} else {
		delete(s.autoReply, sessionID)
	}
	s.mu.Unlock()
	if s.sessions != nil {
		s.sessions.SetAutoReply(sessionID, on)
	}
}

// handleReactionAdded turns a thread's auto-reply switch ON when the 🤖
// emoji lands on the thread's parent message. The reaction itself is never
// dispatched as a turn — it only arms future replies. Every guard that
// fails is a silent no-op: a reaction is ambient, not a command, so an
// off-target 🤖 should not nag the reactor.
//
// Guards: feature enabled · emoji is the trigger · reactor is not the bot ·
// the channel is in ReactionChannels · the reacted item is a thread parent ·
// the thread already has a session · the reactor passes access control.
func (s *Channel) handleReactionAdded(ctx context.Context, ev *slackevents.ReactionAddedEvent) {
	l := log.With().Str("channel", "slack").Str("emoji", ev.Reaction).
		Str("user", ev.User).Str("slack_channel", ev.Item.Channel).
		Str("item_ts", ev.Item.Timestamp).Logger()
	l.Debug().Msg("reaction_added received")

	if ev.Reaction != reactionTrigger {
		l.Debug().Str("want", reactionTrigger).Msg("reaction: ignored — not the trigger emoji")
		return
	}
	cfg := s.snapshot()
	if !cfg.ReactionTriggerEnabled {
		l.Debug().Msg("reaction: ignored — reaction_trigger_enabled is off")
		return
	}
	if s.isBotUser(ev.User) {
		l.Debug().Msg("reaction: ignored — reactor is the bot itself")
		return
	}
	channelID := ev.Item.Channel
	if channelID == "" || !reactionChannelAllowed(cfg, channelID) {
		l.Debug().Str("mode", cfg.ReactionChannelsMode).Msg("reaction: ignored — channel not allowed by ReactionChannels(Mode)")
		return
	}

	// Resolve the thread the reacted message belongs to. The switch lives on
	// the parent only: if the 🤖 is on a reply bubble, ignore it.
	parentTS, isParent := s.reactionThreadParent(channelID, ev.Item.Timestamp)
	if !isParent {
		l.Debug().Str("parent_ts", parentTS).Msg("reaction: ignored — reacted message is not a thread parent (or history lookup failed)")
		return
	}
	sessionID := s.sessionKey(parentTS)

	// Reply-only: the switch governs an existing thread, never boots one.
	if !s.sessionOnDisk(sessionID) {
		l.Debug().Str("session", sessionID).Msg("reaction: ignored — no existing session for this thread (reply-only)")
		return
	}

	// Re-check access: only reactors who could trigger a message may arm
	// the thread for everyone.
	groupIDs, err := s.resolveUserGroups(ev.User)
	if err != nil {
		l.Warn().Err(err).Msg("reaction: resolve groups failed; treating as empty")
	}
	if ok, reason := s.allowedCfg(cfg, ev.User, groupIDs, channelID); !ok {
		l.Debug().Str("reason", reason).Msg("reaction: ignored — reactor failed access control")
		return
	}

	s.setAutoReply(sessionID, true)
	l.Info().Str("thread_ts", parentTS).Str("session", sessionID).Msg("auto-reply switch ON")
	_ = ctx
}

// reactionChannelAllowed reports whether the 🤖 switch is honoured in
// channelID. Mode "all" accepts any channel the bot is in; "whitelist" (the
// default) accepts only the channels in ReactionChannels. An empty/unknown
// mode is treated as whitelist so a misconfigured row fails closed.
func reactionChannelAllowed(cfg agentconfig.SlackChannelConfig, channelID string) bool {
	if cfg.ReactionChannelsMode == "all" {
		return true
	}
	return pickerHas(cfg.ReactionChannels, channelID)
}

// handleReactionRemoved turns a thread's auto-reply switch OFF when the 🤖
// emoji is removed from its parent. A turn already in flight is NOT aborted —
// removing the switch only stops the next reply from being picked up. Cheap
// guards only (emoji + bot); the rest is a map delete that is harmless when
// the key is absent, so no API round-trip is needed here.
func (s *Channel) handleReactionRemoved(ev *slackevents.ReactionRemovedEvent) {
	if ev.Reaction != reactionTrigger || s.isBotUser(ev.User) {
		return
	}
	parentTS := ev.Item.Timestamp
	if parentTS == "" {
		return
	}
	sessionID := s.sessionKey(parentTS)
	if !s.autoReplyOn(sessionID) {
		return
	}
	s.setAutoReply(sessionID, false)
	log.Info().Str("channel", "slack").Str("slack_channel", ev.Item.Channel).Str("thread_ts", parentTS).Str("user", ev.User).Msg("auto-reply switch OFF")
}

// isBotUser reports whether userID is this channel's own bot, so a reaction
// the bot itself places (or any other event it originates) never triggers
// the switch.
func (s *Channel) isBotUser(userID string) bool {
	s.cfgMu.Lock()
	botID := s.botUserID
	s.cfgMu.Unlock()
	return userID != "" && userID == botID
}

// reactionThreadParent resolves the reacted message's owning thread root and
// reports whether the reacted message IS that root. The switch only lives on
// the parent, so a 🤖 on a reply bubble returns isParent=false.
//
// It fetches the single message at itemTS via conversations.history. A
// top-level message has an empty thread_ts (or one equal to its own ts); a
// reply carries its parent's thread_ts. On any API failure we conservatively
// treat the item as a non-parent (isParent=false) so we never arm the wrong
// thread.
func (s *Channel) reactionThreadParent(channelID, itemTS string) (parentTS string, isParent bool) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || channelID == "" || itemTS == "" {
		return "", false
	}
	// Latest+Inclusive+Limit:1 fetches exactly the message at itemTS. Setting
	// Oldest==Latest is unreliable (Slack can return an empty window), so we
	// bound only the top and take the first row.
	resp, err := api.GetConversationHistory(&slackgo.GetConversationHistoryParameters{
		ChannelID: channelID,
		Latest:    itemTS,
		Inclusive: true,
		Limit:     1,
	})
	if err != nil || resp == nil || len(resp.Messages) == 0 {
		log.Warn().Str("channel", "slack").Str("slack_channel", channelID).Str("ts", itemTS).
			Int("messages", func() int {
				if resp == nil {
					return -1
				}
				return len(resp.Messages)
			}()).
			Err(err).Msg("reaction: conversations.history returned nothing — cannot resolve parent")
		return "", false
	}
	m := resp.Messages[0]
	log.Debug().Str("channel", "slack").Str("slack_channel", channelID).Str("item_ts", itemTS).
		Str("msg_ts", m.Timestamp).Str("thread_ts", m.ThreadTimestamp).
		Msg("reaction: resolved reacted message")
	// A parent has no thread_ts, or a thread_ts that equals its own ts.
	if m.ThreadTimestamp == "" || m.ThreadTimestamp == itemTS {
		return itemTS, true
	}
	return m.ThreadTimestamp, false
}

// pingFallbackText stands in for an empty mention/ping so the agent
// greets the user instead of reacting to an empty turn.
const pingFallbackText = "(The user greeted you with no message — greet them briefly and ask how you can help.)"

// normalizeUserText returns a usable prompt: the user's text, or the
// ping fallback when they sent nothing but a bare mention.
func normalizeUserText(text string) string {
	if strings.TrimSpace(text) == "" {
		return pingFallbackText
	}
	return text
}

// senderLabel resolves a Slack user ID into a "Name (@handle, U…)" label,
// cached per user. On a cache miss it calls users.info; if that fails it
// falls back to the bare user ID so the prefix is never empty. The label
// prefixes every inbound turn so the agent knows who spoke — in a shared
// thread, and for matching the sender to a connector when replying.
func (s *Channel) senderLabel(userID string) string {
	if userID == "" {
		return ""
	}
	s.userDisplayMu.RLock()
	label, ok := s.userDisplayCache[userID]
	s.userDisplayMu.RUnlock()
	if ok {
		return label
	}

	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()

	label = userID // fallback when the API is unavailable or the lookup fails
	if api != nil {
		if u, err := api.GetUserInfo(userID); err == nil && u != nil {
			label = formatSenderLabel(userID, u.Name, u.RealName)
		}
	}

	s.userDisplayMu.Lock()
	s.userDisplayCache[userID] = label
	s.userDisplayMu.Unlock()
	return label
}

// formatSenderLabel builds the "Name (@handle, U…)" prefix from the parts a
// users.info lookup returns, degrading gracefully when a part is missing. The
// user ID is always present (it's the fallback), so the result is never empty.
func formatSenderLabel(userID, handle, real string) string {
	switch {
	case real != "" && handle != "":
		return fmt.Sprintf("%s (@%s, %s)", real, handle, userID)
	case handle != "":
		return fmt.Sprintf("@%s (%s)", handle, userID)
	case real != "":
		return fmt.Sprintf("%s (%s)", real, userID)
	default:
		return userID
	}
}

// formatAttachments renders a Slack message's files as a text block appended
// to the user turn, so the agent knows what was attached and has a permalink
// to fetch each file via the slack connector. The bytes are NOT downloaded —
// only the metadata + link are surfaced. Returns "" when there are no files.
//
// Each line carries: title/name, pretty type, human size, and the best
// available link (permalink for in-Slack view; url_private as a fallback the
// connector can GET with the bot token).
func formatAttachments(files []slackgo.File) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Attached files — fetch via the slack connector (files.info / the link) if you need the contents]")
	for _, f := range files {
		name := f.Title
		if name == "" {
			name = f.Name
		}
		link := f.Permalink
		if link == "" {
			link = f.URLPrivate
		}
		b.WriteString("\n- ")
		b.WriteString(name)
		if f.PrettyType != "" {
			b.WriteString(" (")
			b.WriteString(f.PrettyType)
			b.WriteString(")")
		}
		if f.Size > 0 {
			b.WriteString(" · ")
			b.WriteString(humanSize(int64(f.Size)))
		}
		if link != "" {
			b.WriteString(" · ")
			b.WriteString(link)
		}
	}
	return b.String()
}

// humanSize formats a byte count as a compact human-readable string.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
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

// mentionsBot reports whether text contains an @mention of this bot
// (<@BOTID>). A channel message that mentions the bot also arrives as an
// AppMentionEvent, so on an auto-reply thread the duplicate MessageEvent must
// be skipped to avoid dispatching the same message twice. Returns false when
// the bot's own user ID isn't resolved yet (fail open — the auto-reply gate
// still applies), so a brief post-connect window can't drop a real reply.
func (s *Channel) mentionsBot(text string) bool {
	s.cfgMu.Lock()
	botID := s.botUserID
	s.cfgMu.Unlock()
	if botID == "" {
		return false
	}
	return strings.Contains(text, "<@"+botID+">")
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

func (s *Channel) handleMessage(ctx context.Context, ev *slackevents.MessageEvent, files []slackgo.File) {
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
	if ok, reason := s.allowedCfg(cfg, ev.User, groupIDs, ev.Channel); !ok {
		log.Warn().Str("channel", "slack").
			Str("user", ev.User).
			Str("slack_channel", ev.Channel).
			Str("channel_type", ev.ChannelType).
			Strs("groups", groupIDs).
			Str("reason", reason).
			Msg("access denied, ignoring message")
		s.setReaction(reactionBlocked, ev.Channel, ev.TimeStamp, "")
		s.notifyAccessDenied(ctx, ev.User, ev.Channel, reason)
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

	// Namespace the session by instance so two bots (possibly in different
	// workspaces) never collide on an equal threadTS. The bare threadTS is
	// kept on the turn for Slack API calls.
	sessionID := s.sessionKey(threadTS)

	s.mu.Lock()
	old := s.turns[sessionID]
	t := &turn{channelID: ev.Channel, threadTS: threadTS, msgTS: ev.TimeStamp}
	if old != nil {
		t.buf.WriteString(old.buf.String())
		t.hasStarted = old.hasStarted
		// A new turn supersedes the old one: stop its status heartbeat so the
		// goroutine doesn't outlive the turn it was refreshing.
		s.stopStatusAnimation(old)
	}
	s.turns[sessionID] = t
	s.mu.Unlock()

	// Set the status (with our loading_messages) BEFORE spawning the agent —
	// Slack's guidance is to set status immediately when a message arrives,
	// and the loading_messages override only takes effect if it lands before
	// Slack paints its own default loading bubble. startStatusAnimation paints
	// the first frame synchronously and starts the dot-animation ticker.
	s.startStatusAnimation(sessionID)

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
		cur := s.turns[sessionID]
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

	userText := normalizeUserText(ev.Text)
	// Prefix the sender so the agent always knows who spoke — essential in
	// multi-user threads and for matching the sender to a connector (bot vs
	// the user's SSO-connected account) when replying. Skip for the bare-ping
	// fallback, which is a meta instruction rather than something the user said.
	if strings.TrimSpace(ev.Text) != "" {
		if who := s.senderLabel(ev.User); who != "" {
			userText = who + ": " + userText
		}
	}
	// Append an attachment manifest so the agent knows the user posted files
	// (images, PDFs, …) and has a permalink to fetch each one — the bytes
	// themselves aren't downloaded here. Empty when the message had no files.
	userText += formatAttachments(files)
	isNewSession := !s.sessionOnDisk(sessionID)
	if isNewSession {
		ctxText := s.buildSessionContext(ev, threadTS)
		if ctxText != "" {
			if err := s.sendFn(context.Background(), sessionID, "main", "slack", "system", ctxText); err != nil {
				log.Warn().Str("channel", "slack").Str("session", sessionID).Err(err).Msg("inject session context failed")
			}
		}
		if hook := s.onSessionStart; hook != nil {
			hook(sessionID, "slack", ctxText)
		}
	}

	if err := s.sendFn(context.Background(), sessionID, "main", "slack", "user", userText); err != nil {
		log.Error().Str("channel", "slack").Str("session", sessionID).Err(err).Msg("pool send failed")
		s.cancelQueueTimer(sessionID, ev.Channel, ev.TimeStamp)
		s.setReaction(reactionError, ev.Channel, ev.TimeStamp, "")
		s.postReply(ev.Channel, threadTS, "Agent error: could not queue message. Check the dashboard for details.")
		return
	}
	// Arm auto-reply for a brand-new CHANNEL thread, after the send has
	// created the session on disk so the persisted flag sticks. The agent
	// marks the parent with 🤖; every later reply is then answered without a
	// mention. The marker doubles as the on/off switch — remove it to stop,
	// re-add to resume (handleReactionRemoved / handleReactionAdded). DMs are
	// already always-on, so they get neither the switch nor the marker.
	if isNewSession && ev.ChannelType == "channel" && cfg.ReactionTriggerEnabled && reactionChannelAllowed(cfg, ev.Channel) {
		s.setAutoReply(sessionID, true)
		// threadTS is the parent message for a new thread; mark it.
		s.setReaction(reactionTrigger, ev.Channel, threadTS, "")
		log.Info().Str("channel", "slack").Str("slack_channel", ev.Channel).
			Str("thread_ts", threadTS).Str("session", sessionID).
			Msg("auto-reply armed on new thread (🤖 marker posted)")
	}
	// Stamp the session owner once, on the first message that creates the
	// session. wickUserIDFn hits the DB, so skipping it on every follow-up
	// reply (where the owner is already set) avoids needless per-message
	// queries. EnsureSessionOwner is a no-op if an owner already exists.
	if isNewSession && s.ownerFn != nil {
		if s.wickUserIDFn != nil {
			if wickUserID, ok := s.wickUserIDFn(context.Background(), ev.User); ok {
				s.ownerFn(context.Background(), sessionID, wickUserID)
			}
		}
		if s.ownerUserID != "" {
			s.ownerFn(context.Background(), sessionID, s.ownerUserID)
		}
	}
	// Message accepted by the pool: cancel pending queue timer (and remove
	// ⏳ if it had already fired), then surface the "thinking" banner so
	// the operator sees a working signal even while the agent is still
	// thinking. No "running" emoji — agent status lives in Wick's web UI
	// and the assistant thread banner.
	s.cancelQueueTimer(sessionID, ev.Channel, ev.TimeStamp)
	// Banner animation was already started above (before sendFn) so the
	// loading_messages override lands before Slack's default bubble. Nothing
	// to do here.
}

// paintBubble updates the two independent slots (proven to render separately):
//   - footer (status field)      = coarse state + animated dots, e.g. "Working.."
//   - bubble (loading_messages[]) = recent activity trace, clipped per line;
//     Slack rotates through the entries.
func (s *Channel) paintBubble(channelID, threadTS, label string, trace []string, dot int) {
	if channelID == "" {
		return
	}
	footerLoadingText := footerState(label) + strings.Repeat(".", dot%4)
	s.setAssistantStatusWithLoading(channelID, threadTS, footerLoadingText, bubbleLoadingMessages(trace))
}

// bubbleLoadingMessages returns the recent trace (last traceMax) as the
// loading_messages array — one entry per activity line, which Slack rotates
// through in the bubble. Each entry is clipped to bubbleLineMax. Falls back to
// "Thinking" when empty.
func bubbleLoadingMessages(trace []string) []string {
	if len(trace) == 0 {
		return []string{footerThinking}
	}
	out := make([]string, 0, len(trace))
	for _, line := range trace {
		if r := []rune(line); len(r) > bubbleLineMax {
			line = strings.TrimSpace(string(r[:bubbleLineMax])) + "…"
		}
		out = append(out, line)
	}
	return out
}

// footerState maps a detailed activity label to a short footer state:
// empty → Idle; "Thinking" → Thinking; anything else (a tool line) → Working.
func footerState(label string) string {
	switch label {
	case "":
		return footerIdle
	case footerThinking:
		return footerThinking
	default:
		return footerWorking
	}
}

// setStatusLabel updates the phase shown in the loading bubble for sessionKey
// and repaints immediately on a real change so a phase switch is visible
// without waiting for the next ticker tick. Starts the keep-alive animation
// lazily if it is not already running.
func (s *Channel) setStatusLabel(sessionKey, label string) {
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil {
		s.mu.Unlock()
		return
	}
	changed := t.statusLabel != label
	t.statusLabel = label
	// Append to the rotating trace on a real change (skip consecutive dups),
	// keeping only the last traceMax entries.
	if changed && label != "" && (len(t.trace) == 0 || t.trace[len(t.trace)-1] != label) {
		t.trace = append(t.trace, label)
		if len(t.trace) > traceMax {
			t.trace = t.trace[len(t.trace)-traceMax:]
		}
	}
	channelID, threadTS, dot := t.channelID, t.threadTS, t.dotPhase
	traceCopy := append([]string(nil), t.trace...)
	running := t.statusTicker != nil
	s.mu.Unlock()

	if changed {
		s.paintBubble(channelID, threadTS, label, traceCopy, dot)
	}
	if !running {
		s.startStatusAnimation(sessionKey)
	}
}

// startStatusAnimation launches a goroutine that re-asserts the loading
// bubble every statusAnimInterval while sessionID's turn is still current.
// It is the keep-alive (Slack drops the status after ~2 minutes) and re-sends
// loading_messages so the bubble never reverts to Slack's defaults. No-op if
// already running. Stopped by stopStatusAnimation (Done/Error) or when the
// turn is replaced/removed.
func (s *Channel) startStatusAnimation(sessionID string) {
	s.mu.Lock()
	t := s.turns[sessionID]
	if t == nil || t.statusTicker != nil {
		s.mu.Unlock()
		return
	}
	if t.statusLabel == "" {
		t.statusLabel = statusLabelThinking
	}
	ticker := time.NewTicker(statusAnimInterval)
	stop := make(chan struct{})
	t.statusTicker = ticker
	t.statusStop = stop
	channelID := t.channelID
	threadTS := t.threadTS
	firstLabel := t.statusLabel
	firstDot := t.dotPhase
	firstTrace := append([]string(nil), t.trace...)
	s.mu.Unlock()

	// Paint immediately so the bubble appears without waiting a full interval.
	s.paintBubble(channelID, threadTS, firstLabel, firstTrace, firstDot)

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Only repaint while this turn is still current; a replaced
				// turn cancels via stopStatusAnimation, but guard anyway.
				s.mu.Lock()
				cur := s.turns[sessionID]
				stillCurrent := cur != nil && cur.statusStop == stop
				var label string
				var dot int
				var traceCopy []string
				if stillCurrent {
					cur.dotPhase++
					label = cur.statusLabel
					dot = cur.dotPhase
					traceCopy = append([]string(nil), cur.trace...)
				}
				s.mu.Unlock()
				if !stillCurrent {
					return
				}
				s.paintBubble(channelID, threadTS, label, traceCopy, dot)
			}
		}
	}()
}

// stopStatusAnimation halts the banner-animation goroutine for the turn, if
// any. Safe to call when no animation is running.
func (s *Channel) stopStatusAnimation(t *turn) {
	if t == nil || t.statusTicker == nil {
		return
	}
	t.statusTicker.Stop()
	close(t.statusStop)
	t.statusTicker = nil
	t.statusStop = nil
}

// startEditTicker launches a goroutine that flushes the accumulated text
// buffer to the live Slack message every streamEditInterval, so the reply
// grows in place (chat.update) rather than appearing once at the end. It is
// a no-op if a ticker is already running for the turn. Mirrors
// startStatusAnimation: the same mu guard and stillCurrent check, stopped by
// stopEditTicker on Done/Error or when the turn is replaced.
func (s *Channel) startEditTicker(sessionKey string) {
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil || t.editTicker != nil {
		s.mu.Unlock()
		return
	}
	ticker := time.NewTicker(streamEditInterval)
	stop := make(chan struct{})
	t.editTicker = ticker
	t.editStop = stop
	s.mu.Unlock()

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				cur := s.turns[sessionKey]
				stillCurrent := cur != nil && cur.editStop == stop
				s.mu.Unlock()
				if !stillCurrent {
					return
				}
				s.flushLiveMessage(sessionKey)
			}
		}
	}()
}

// stopEditTicker halts the live-edit goroutine for the turn, if any. Safe to
// call when no ticker is running.
func (s *Channel) stopEditTicker(t *turn) {
	if t == nil || t.editTicker == nil {
		return
	}
	t.editTicker.Stop()
	close(t.editStop)
	t.editTicker = nil
	t.editStop = nil
}

// flushLiveMessage posts (first call) or edits (subsequent calls) the live
// streaming reply for sessionKey from the current contents of turn.buf. It
// shows only the first chunk while streaming — overflow is reconciled into
// continuation replies at Done. No-op when the buffer is unchanged since the
// last flush, empty, or the turn is gone.
func (s *Channel) flushLiveMessage(sessionKey string) {
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil {
		s.mu.Unlock()
		return
	}
	body := t.buf.String()
	channelID := t.channelID
	threadTS := t.threadTS
	liveTS := t.liveTS
	lastSent := t.lastSent
	s.mu.Unlock()

	if body == "" || body == lastSent {
		return
	}
	// While streaming, only the first chunk is shown in the live message;
	// any overflow lands as continuation replies during the Done reconcile.
	shown := body
	if len(shown) > maxSlackChunk {
		shown = chunkText(body, maxSlackChunk)[0]
	}

	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}

	if liveTS == "" {
		var newTS string
		s.withBackoff(func() error {
			_, ts, err := api.PostMessage(
				channelID,
				slackgo.MsgOptionText(shown, false),
				slackgo.MsgOptionTS(threadTS),
			)
			if err == nil {
				newTS = ts
			}
			return err
		})
		if newTS == "" {
			return // post failed; retry on next tick
		}
		s.mu.Lock()
		if cur := s.turns[sessionKey]; cur != nil {
			cur.liveTS = newTS
			cur.lastSent = shown
		}
		s.mu.Unlock()
		return
	}

	s.withBackoff(func() error {
		_, _, _, err := api.UpdateMessage(
			channelID, liveTS,
			slackgo.MsgOptionText(shown, false),
		)
		return err
	})
	s.mu.Lock()
	if cur := s.turns[sessionKey]; cur != nil {
		cur.lastSent = shown
	}
	s.mu.Unlock()
}

// cancelQueueTimer stops the pending queue-reaction timer for the named
// turn. sessionID is the namespaced session key (turns map key); channelID
// and msgTS are native Slack identifiers for the reaction removal. If the
// timer already fired and the ⏳ reaction was added, it is removed too.
// Returns true when the reaction was visible at call time.
func (s *Channel) cancelQueueTimer(sessionID, channelID, msgTS string) bool {
	s.mu.Lock()
	t := s.turns[sessionID]
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
// sessionKey is the namespaced session key (turns map key); replies and the
// status banner use the turn's native threadTS, never the namespaced key.
func (s *Channel) NotifyState(sessionKey, state, text string) {
	s.mu.Lock()
	t := s.turns[sessionKey]
	var channelID, threadTS, msgTS, liveTS, lastSent string
	if t != nil {
		channelID = t.channelID
		threadTS = t.threadTS
		msgTS = t.msgTS
		liveTS = t.liveTS
		lastSent = t.lastSent
	}
	s.mu.Unlock()
	if channelID == "" {
		return
	}

	switch state {
	case "running":
		// No-op for the banner: the animation ticker owns the status text and
		// the phase label is set from OnAgentEvent. Kept as a state for callers
		// but there is nothing extra to paint here.
	case "done":
		s.setAssistantStatus(channelID, threadTS, "")
		s.finalizeReply(sessionKey, channelID, threadTS, text, liveTS, lastSent)
	case "blocked":
		s.setReaction(reactionBlocked, channelID, msgTS, "")
		s.setAssistantStatus(channelID, threadTS, "")
		note := text
		if note == "" {
			note = "Agent turn completed with blocked commands. See the dashboard for details."
		} else {
			note += "\n\n_Some commands were blocked — see the dashboard for details._"
		}
		s.postChunked(channelID, threadTS, note)
	case "error":
		s.setReaction(reactionError, channelID, msgTS, "")
		s.setAssistantStatus(channelID, threadTS, "")
		msg := "Agent error."
		if text != "" {
			msg = fmt.Sprintf("Agent error: %s", text)
		}
		s.postReply(channelID, threadTS, msg+"\n\nSee the dashboard for details.")
	}
}

// finalizeReply settles the turn's reply at Done. It reconciles the live
// streaming message (if one was posted) against the final text:
//   - No live message (turn finished before the first edit tick) → post the
//     whole text fresh via postChunked, identical to the old behaviour.
//   - Live message exists and the final text fits one chunk → update it only
//     when the body changed since the last flush (text == lastSent → skip,
//     0 API calls).
//   - Live message exists but the final text overflows one chunk → update the
//     live message to the first chunk and post the remainder as continuation
//     replies.
//
// liveTS/lastSent are passed by the caller (read under mu); the turn's live
// state is cleared afterwards so a follow-up turn on the same session starts
// a fresh message.
func (s *Channel) finalizeReply(sessionKey, channelID, threadTS, text, liveTS, lastSent string) {
	defer func() {
		s.mu.Lock()
		if cur := s.turns[sessionKey]; cur != nil {
			cur.liveTS = ""
			cur.lastSent = ""
		}
		s.mu.Unlock()
	}()

	plan := reconcilePlan(text, liveTS, lastSent)
	if plan.postFresh {
		s.postChunked(channelID, threadTS, text)
		return
	}

	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}

	if plan.update {
		s.withBackoff(func() error {
			_, _, _, err := api.UpdateMessage(
				channelID, liveTS,
				slackgo.MsgOptionText(plan.first, false),
			)
			return err
		})
	}
	for _, chunk := range plan.continuations {
		s.postReply(channelID, threadTS, "_(cont.)_\n"+chunk)
	}
}

// replyPlan is the decision finalizeReply executes: whether to post the whole
// text fresh (no live message), update the live message to first, and which
// overflow chunks to post as continuation replies. Pure so it can be tested
// without the Slack client.
type replyPlan struct {
	postFresh     bool     // post the whole text via postChunked (no live msg)
	update        bool     // chat.update the live message to first
	first         string   // first chunk shown in the live message
	continuations []string // overflow chunks posted as "_(cont.)_" replies
}

// reconcilePlan computes how to settle a finished turn given the final text,
// the live message ts (empty = never posted), and the body last flushed to it.
//   - text == ""        → nothing.
//   - liveTS == ""      → postFresh (identical to the pre-streaming behaviour).
//   - first == lastSent → skip the update (0 calls); still post any overflow.
//   - otherwise         → update to first + post overflow as continuations.
func reconcilePlan(text, liveTS, lastSent string) replyPlan {
	if text == "" {
		return replyPlan{}
	}
	if liveTS == "" {
		return replyPlan{postFresh: true}
	}
	chunks := chunkText(text, maxSlackChunk)
	return replyPlan{
		update:        chunks[0] != lastSent,
		first:         chunks[0],
		continuations: chunks[1:],
	}
}

// setAssistantStatus updates the Slack AI thread status banner via
// assistant.threads.setStatus. status="" clears the banner. Errors are
// logged at debug only — the workspace may not have Slack AI enabled
// or the bot may lack the assistant:write/chat:write scope; reaction
// emojis remain as the primary progress signal.
func (s *Channel) setAssistantStatus(channelID, threadTS, status string) {
	s.setAssistantStatusWithLoading(channelID, threadTS, status, nil)
}

// setAssistantStatusWithLoading is setAssistantStatus plus an optional
// loading_messages list. Slack rotates loading_messages in the thread's
// loading bubble; passing our own list replaces Slack's built-in defaults
// ("Reviewing findings…", "Processing…", …). It must be sent on EVERY status
// update — omitting it on a later call lets Slack fall back to its defaults,
// so the custom bubble would flash for one interval and then revert.
func (s *Channel) setAssistantStatusWithLoading(channelID, threadTS, status string, loadingMessages []string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || channelID == "" || threadTS == "" {
		return
	}
	err := api.SetAssistantThreadsStatus(slackgo.AssistantThreadsSetStatusParameters{
		ChannelID:       channelID,
		ThreadTS:        threadTS,
		Status:          status,
		LoadingMessages: loadingMessages,
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
			// Begin live-editing the reply: post once now, then chat.update
			// every streamEditInterval as more text streams in.
			s.startEditTicker(sessionKey)
		}
		// Streaming text → "Thinking" phase. setStatusLabel only repaints on a
		// real change, so this is cheap on every delta.
		s.setStatusLabel(sessionKey, statusLabelThinking)

	case event.Thinking:
		// Reasoning delta → "Thinking" phase (animated banner shows the dots).
		s.setStatusLabel(sessionKey, statusLabelThinking)

	case event.ToolUse:
		// A tool is starting → show what it's doing ("Running: npm test",
		// "Reading slack.go", …). The label is held until the next phase; the
		// animation ticker supplies the moving dots and the keep-alive.
		s.setStatusLabel(sessionKey, toolStatusLabel(ev.ToolName, ev.ToolInput))

	case event.ToolResult:
		// Tool finished → back to the generic working phase while the agent
		// decides the next step.
		s.setStatusLabel(sessionKey, statusLabelWorking)

	case event.Done:
		var text string
		hasError := ev.ErrorMsg != ""
		s.mu.Lock()
		t := s.turns[sessionKey]
		if t == nil {
			s.mu.Unlock()
			return
		}
		s.stopStatusAnimation(t)
		s.stopEditTicker(t)
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
		t := s.turns[sessionKey]
		if t != nil {
			s.stopStatusAnimation(t)
			s.stopEditTicker(t)
		}
		s.mu.Unlock()
		if t == nil {
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

	// Resolve channel info.
	channelName := ev.Channel
	if info, err := api.GetConversationInfo(&slackgo.GetConversationInfoInput{ChannelID: ev.Channel}); err == nil && info != nil {
		if info.Name != "" {
			channelName = info.Name
		}
	}

	// Resolve user info.
	userHandle := ev.User
	userReal := ""
	teamName := ""
	if u, err := api.GetUserInfo(ev.User); err == nil && u != nil {
		if u.Name != "" {
			userHandle = u.Name
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

	// Pre-resolve the DM channel so Claude can send DMs without an extra API call.
	// 3-second timeout; failure is non-fatal — section is omitted.
	dmChannelID := ""
	dmCtx, dmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer dmCancel()
	if ch, _, _, err := api.OpenConversationContext(dmCtx, &slackgo.OpenConversationParameters{
		Users:    []string{ev.User},
		ReturnIM: true,
	}); err == nil && ch != nil {
		dmChannelID = ch.ID
	} else if err != nil {
		log.Warn().Str("channel", "slack").Str("user", ev.User).Err(err).
			Msg("buildSessionContext: conversations.open failed; omitting DM channel ID")
	}

	// Check if the mentioning user has a connector user token configured.
	// If yes, DMs sent via the proxy will automatically appear as from them.
	// s.cfgMu.Lock()
	// connTokFn := s.connectorToken
	// s.cfgMu.Unlock()
	// hasUserToken := false
	// if connTokFn != nil {
	// 	tok := s.resolveUserToken(dmCtx, ev.User, connTokFn)
	// 	hasUserToken = tok != ""
	// }

	// Fetch first 50 workspace members for a username→user_id directory.
	// 3-second timeout; failure is non-fatal — section is omitted.
	var memberEntries []string
	membersCtx, membersCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer membersCancel()
	if members, err := api.GetUsersContext(membersCtx, slackgo.GetUsersOptionLimit(50)); err == nil {
		for _, m := range members {
			if m.Deleted || m.IsBot {
				continue
			}
			memberEntries = append(memberEntries, fmt.Sprintf("@%s (%s)", m.Name, m.ID))
		}
	} else {
		log.Warn().Str("channel", "slack").Err(err).
			Msg("buildSessionContext: users.list failed; omitting member directory")
	}

	lines := []string{"[Slack thread context — injected by wick]"}
	if teamName != "" {
		lines = append(lines, "Workspace: "+teamName)
	}
	lines = append(lines,
		fmt.Sprintf("Channel: #%s [%s]", channelName, ev.Channel),
		fmt.Sprintf("User: @%s (%s) [%s]", userHandle, userReal, ev.User),
	)
	if dmChannelID != "" {
		lines = append(lines, "DM channel: "+dmChannelID)
	}
	lines = append(lines, "Thread: "+threadTS)
	if permalink != "" {
		lines = append(lines, "Link: "+permalink)
	}

	if len(memberEntries) > 0 {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("Workspace members (first %d): %s", len(memberEntries), strings.Join(memberEntries, " | ")))
	}

	// lines = append(lines, "")
	// lines = append(lines, "IMPORTANT: To send Slack messages, use wick proxy only — do NOT use Slack MCP tools.")

	// // Shared curl base — always include X-Wick-Session-User so the proxy
	// // can auto-inject sender_user_id without Claude needing to specify it.
	// // When re-enabling with multiple Slack bots configured, ALSO send
	// // -H "X-Wick-Session-Id: <sessionID>" so the registry fan-in dispatcher
	// // (Channel.OwnsRequest) routes the proxy call to THIS bot and not a
	// // sibling instance sharing the /integrations/slack/send route.
	// curlBase := fmt.Sprintf(
	// 	`curl -s -X POST "http://localhost:$WICK_PORT/integrations/slack/send" -H "Content-Type: application/json" -H "X-Wick-Session-User: %s"`,
	// 	ev.User,
	// )

	// if hasUserToken {
	// 	lines = append(lines, fmt.Sprintf("Your user token is configured — messages will appear as from @%s.", userHandle))
	// 	lines = append(lines, "To send to any channel or thread (appears from you):")
	// 	lines = append(lines, fmt.Sprintf(`  %s -d '{"channel_id":"CHANNEL_ID","text":"YOUR MESSAGE"}'`, curlBase))
	// 	lines = append(lines, "To DM another user (appears from you):")
	// 	lines = append(lines, fmt.Sprintf(`  %s -d '{"target_user_id":"THEIR_USER_ID","text":"YOUR MESSAGE"}'`, curlBase))
	// 	lines = append(lines, "  Replace THEIR_USER_ID with the ID from the member directory above.")
	// 	lines = append(lines, "If DM fails with open_dm_failed/missing_scope: post to the original channel thread instead:")
	// 	lines = append(lines, fmt.Sprintf(`  %s -d '{"channel_id":"%s","text":"<@THEIR_USER_ID> YOUR MESSAGE"}'`, curlBase, ev.Channel))
	// } else {
	// 	lines = append(lines, "To send to any channel or thread (appears from bot):")
	// 	lines = append(lines, fmt.Sprintf(`  %s -d '{"channel_id":"CHANNEL_ID","text":"YOUR MESSAGE"}'`, curlBase))
	// 	lines = append(lines, "To DM another user (appears from bot):")
	// 	lines = append(lines, fmt.Sprintf(`  %s -d '{"target_user_id":"THEIR_USER_ID","text":"YOUR MESSAGE"}'`, curlBase))
	// 	lines = append(lines, "If DM fails: post to original channel thread with mention instead:")
	// 	lines = append(lines, fmt.Sprintf(`  %s -d '{"channel_id":"%s","text":"<@THEIR_USER_ID> YOUR MESSAGE"}'`, curlBase, ev.Channel))
	// }

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
	threadTS := t.threadTS
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

func (s *Channel) handleInteraction(ctx context.Context, cb slackgo.InteractionCallback) {
	// Workflow surface first — emit a typed event for every interaction
	// type so workflows can route by callback_id / action_id without
	// caring about the gate-approval channel-side hijack below.
	s.emitInteractionWorkflow(ctx, cb)

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
		// Thread the ephemeral under the native thread_ts, not the namespaced
		// session key. Fall back to the click's own message ts if the turn is
		// gone (session expired between prompt and click).
		s.mu.Lock()
		threadTS := cb.Message.Timestamp
		if t := s.turns[sessionID]; t != nil && t.threadTS != "" {
			threadTS = t.threadTS
		}
		s.mu.Unlock()
		s.cfgMu.Lock()
		api := s.api
		s.cfgMu.Unlock()
		if api != nil {
			_, err := api.PostEphemeral(cb.Channel.ID, cb.User.ID,
				slackgo.MsgOptionText("Not authorized to approve this action.", false),
				slackgo.MsgOptionTS(threadTS),
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
		ok, _ := s.allowedCfg(cfg, userID, groupIDs, "")
		return ok
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

// toolStatusLabel turns a ToolUse event into a banner phase like
// "Running: npm test", "Reading slack.go", or "Searching". It maps the tool
// name to a verb and pulls one identifying field out of the JSON tool input
// (command / file_path / pattern / …) as a short summary. The animated banner
// appends the cycling dot suffix; this returns the label only (no trailing
// dots). Falls back to a generic per-tool verb when the input is missing or
// unparseable, and to "Working" for unknown tools. Pure — unit-tested.
func toolStatusLabel(toolName, toolInput string) string {
	const maxSummary = 100

	var args map[string]any
	if toolInput != "" {
		_ = json.Unmarshal([]byte(toolInput), &args)
	}
	str := func(key string) string {
		if v, ok := args[key].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	// Collapse whitespace/newlines, strip characters Slack rejects in the
	// assistant status field (it returns invalid_arguments for quotes and
	// other shell punctuation), and clip so a multi-line command stays a
	// single short banner line.
	summarize := func(v string) string {
		v = strings.Map(func(r rune) rune {
			switch r {
			case '"', '`', '\'', '$', ';', '\\', '\n', '\t', '\r':
				return ' '
			}
			return r
		}, v)
		v = strings.Join(strings.Fields(v), " ")
		if r := []rune(v); len(r) > maxSummary {
			v = strings.TrimSpace(string(r[:maxSummary])) + "…"
		}
		return v
	}
	base := func(p string) string {
		p = strings.TrimRight(p, "/\\")
		if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
			return p[i+1:]
		}
		return p
	}
	withSummary := func(verb, raw string) string {
		if v := summarize(raw); v != "" {
			return verb + ": " + v
		}
		return verb
	}

	switch toolName {
	case "Bash":
		return withSummary("Running", str("command"))
	case "Read":
		if f := base(str("file_path")); f != "" {
			return "Reading " + f
		}
		return "Reading"
	case "Edit", "Write", "NotebookEdit":
		if f := base(str("file_path")); f != "" {
			return "Editing " + f
		}
		return "Editing"
	case "Grep":
		return withSummary("Searching", str("pattern"))
	case "Glob":
		return withSummary("Finding files", str("pattern"))
	case "WebFetch":
		return withSummary("Fetching", str("url"))
	case "WebSearch":
		return withSummary("Searching the web", str("query"))
	case "Task", "Agent":
		return withSummary("Delegating", str("description"))
	case "TodoWrite":
		return "Updating plan"
	case "":
		return statusLabelWorking
	default:
		// Unknown/MCP tool — show its name so the operator still sees activity.
		return "Running " + toolName
	}
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
//
// allowedCfg reports whether the user may trigger the agent, plus a short
// machine reason ("identity" / "channels") when denied. reason is "" when
// allowed. Used to tailor the access-denied DM.
func (s *Channel) allowedCfg(cfg agentconfig.SlackChannelConfig, userID string, groupIDs []string, channelID string) (bool, string) {
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
		return false, "identity"
	}
	if channelID != "" && cfg.ChannelsMode == "whitelist" && !pickerHas(cfg.AllowedChannels, channelID) {
		log.Debug().Str("channel", "slack").Str("reject", "channels").Str("slack_channel", channelID).Msg("denied by channels whitelist")
		return false, "channels"
	}
	return true, ""
}

// notifyAccessDenied DMs the user who was blocked, explaining why, so the
// 🚫 reaction on their message isn't a silent dead-end. Best-effort: any
// API failure is logged and swallowed (the reaction already conveys the
// block). reason comes from allowedCfg ("identity" / "channels").
func (s *Channel) notifyAccessDenied(ctx context.Context, userID, channelID, reason string) {
	if userID == "" {
		return
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}

	var msg string
	switch reason {
	case "channels":
		msg = "🚫 I can't run in this channel — it isn't on the allow-list for this workspace. " +
			"Ask an admin to add it, or message me in an approved channel."
	default: // "identity"
		msg = "🚫 You're not on the allow-list to use this assistant in this workspace. " +
			"Ask an admin to grant you (or one of your user groups) access."
	}

	dmCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ch, _, _, err := api.OpenConversationContext(dmCtx, &slackgo.OpenConversationParameters{
		Users:    []string{userID},
		ReturnIM: true,
	})
	if err != nil || ch == nil {
		log.Warn().Str("channel", "slack").Str("user", userID).Err(err).
			Msg("access-denied DM: conversations.open failed")
		return
	}
	if _, _, err := api.PostMessageContext(dmCtx, ch.ID, slackgo.MsgOptionText(msg, false)); err != nil {
		log.Warn().Str("channel", "slack").Str("user", userID).Err(err).
			Msg("access-denied DM: postMessage failed")
	}
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
	// Dashboard links point at the namespaced on-disk session id, not the
	// bare threadTS — that's the folder the pool actually wrote.
	sessionID := s.sessionKey(threadTS)
	switch meta.Cmd {
	case "dashboard", "link":
		url := s.dashboardURL(sessionID)
		s.postReply(channelID, threadTS, fmt.Sprintf("Dashboard: %s", url))
	case "status":
		s.postReply(channelID, threadTS, "_Use the dashboard to view real-time agent status._\n"+s.dashboardURL(sessionID))
	case "reset":
		s.postReply(channelID, threadTS, "_Reset acknowledged. The next message will start a fresh context._")
	case "agent":
		if meta.Arg == "" {
			s.postReply(channelID, threadTS, "_Usage: /agent <name>_")
			return
		}
		s.postReply(channelID, threadTS, fmt.Sprintf("_Switching to agent `%s` is not yet wired. Coming soon._", meta.Arg))
	case "log":
		s.postReply(channelID, threadTS, "_Command log: see the dashboard for full details._\n"+s.dashboardURL(sessionID))
	}
}

func (s *Channel) dashboardURL(sessionID string) string {
	s.cfgMu.Lock()
	pubURL := s.pubURL
	s.cfgMu.Unlock()
	base := strings.TrimRight(pubURL, "/")
	if base == "" {
		return "(dashboard URL not configured — set PublicURL in Settings → Agents)"
	}
	return fmt.Sprintf("%s/tools/agents/sessions/%s", base, url.PathEscape(sessionID))
}
