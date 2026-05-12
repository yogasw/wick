// Package rest implements an OpenAI Chat Completions compatible HTTP
// channel for the agents pool. Clients use any OpenAI SDK pointed at
// http://<wick>/integrations/rest/v1 with a wick Personal Access Token
// as the Bearer.
//
// Request shape (subset of OpenAI):
//
//	POST /integrations/rest/v1/chat/completions
//	Authorization: Bearer wick_pat_...
//	{ "model": "wick", "messages": [...] }
//
// Behaviour: stateless — every request spawns a brand-new wick session
// (UUID), flattens the messages array into a single prompt (system /
// prior assistant / earlier user turns are tagged, the final user turn
// is the live prompt), and returns the aggregated assistant text as a
// chat.completion object. Conversation continuity is the client's job:
// re-send the full history on each call, exactly like the upstream
// OpenAI API. The `user` field is accepted for OpenAI parity but only
// used as an audit label — it does not key the session. Streaming and
// interactive approvals are unsupported (approvals auto-block) so REST
// clients never hang waiting for a button-press they cannot deliver.
package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
)

// Authenticator validates a plaintext Bearer token and returns the owning
// user_id. Implemented by accesstoken.Service.Authenticate.
type Authenticator interface {
	Authenticate(ctx context.Context, plain string) (userID string, err error)
}

// turn holds per-session state for one in-flight request. Done is closed
// when the Done event arrives.
type turn struct {
	buf      strings.Builder
	done     chan struct{}
	errMsg   string
	blocked  bool
	finished bool
}

// Channel implements agentchannels.Channel for an OpenAI-compatible HTTP
// endpoint. No connection lifecycle — Start/Stop are no-ops; the HTTP
// handler is mounted by the registry and lives for the server's lifetime.
type Channel struct {
	auth Authenticator

	cfgMu sync.Mutex
	cfg   agentconfig.RestChannelConfig

	sendFn         agentchannels.SendFunc
	approveFn      agentchannels.ApproveFn
	sessions       agentchannels.SessionChecker
	onSessionStart agentchannels.SessionStartHook

	mu    sync.Mutex
	turns map[string]*turn
}

// New constructs a REST Channel. auth resolves the Bearer on every
// inbound request; nil disables the channel.
func New(cfg agentconfig.RestChannelConfig, auth Authenticator) *Channel {
	return &Channel{
		cfg:   cfg,
		auth:  auth,
		turns: make(map[string]*turn),
	}
}

// Name satisfies Channel.
func (c *Channel) Name() string { return "rest" }

// IsConfigured returns true when the operator has flipped the enable
// switch in the UI. Auth is per-request, so there is no token to check.
func (c *Channel) IsConfigured() bool {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.cfg.Enabled == "true" && c.auth != nil
}

// Start is a no-op — the HTTP handler is mounted by the registry and
// served by the public mux. Blocks until ctx is done so the registry's
// goroutine accounting stays consistent.
func (c *Channel) Start(ctx context.Context) error {
	if !c.IsConfigured() {
		return fmt.Errorf("rest: not enabled")
	}
	log.Info().Str("channel", "rest").Msg("started — POST /integrations/rest/v1/chat/completions")
	<-ctx.Done()
	return nil
}

// Stop is a no-op.
func (c *Channel) Stop() {}

// Reload swaps the active config. Safe to call concurrently with serving.
func (c *Channel) Reload(_ context.Context, cfg agentconfig.RestChannelConfig) {
	c.cfgMu.Lock()
	c.cfg = cfg
	c.cfgMu.Unlock()
	log.Info().Str("channel", "rest").Str("enabled", cfg.Enabled).Msg("reload: applied new config")
}

// Setter interfaces ------------------------------------------------------

// SetSendFunc satisfies channels.SendFuncSetter.
func (c *Channel) SetSendFunc(fn agentchannels.SendFunc) { c.sendFn = fn }

// SetApproveFn satisfies channels.ApproveFnSetter — REST uses it to
// auto-block any approval request that surfaces during a request.
func (c *Channel) SetApproveFn(fn agentchannels.ApproveFn) { c.approveFn = fn }

// SetSessionChecker satisfies channels.SessionCheckerSetter.
func (c *Channel) SetSessionChecker(s agentchannels.SessionChecker) { c.sessions = s }

// SetSessionStartHook satisfies channels.SessionStartHookSetter.
func (c *Channel) SetSessionStartHook(fn agentchannels.SessionStartHook) { c.onSessionStart = fn }

// HTTPHandlerProvider ----------------------------------------------------

// HTTPPath satisfies channels.HTTPHandlerProvider.
func (c *Channel) HTTPPath() string {
	return "POST /integrations/rest/v1/chat/completions"
}

// HTTPHandler satisfies channels.HTTPHandlerProvider.
func (c *Channel) HTTPHandler() http.Handler {
	return http.HandlerFunc(c.handleChatCompletions)
}

// AgentEventReceiver -----------------------------------------------------

// OnAgentEvent satisfies channels.AgentEventReceiver — accumulates text
// for the in-flight request and signals Done via the turn's done channel.
func (c *Channel) OnAgentEvent(sessionID string, ev event.AgentEvent) {
	c.mu.Lock()
	tn := c.turns[sessionID]
	c.mu.Unlock()
	if tn == nil {
		return
	}
	switch ev.Type {
	case event.TextDelta:
		c.mu.Lock()
		tn.buf.WriteString(ev.Text)
		c.mu.Unlock()
	case event.Done:
		c.mu.Lock()
		if !tn.finished {
			tn.finished = true
			if ev.ErrorMsg != "" {
				tn.errMsg = ev.ErrorMsg
			}
			close(tn.done)
		}
		c.mu.Unlock()
	case event.Error:
		c.mu.Lock()
		if !tn.finished {
			tn.finished = true
			tn.errMsg = ev.ErrorMsg
			if tn.errMsg == "" {
				tn.errMsg = ev.Text
			}
			close(tn.done)
		}
		c.mu.Unlock()
	}
}

// ApprovalReceiver -------------------------------------------------------

// OnApprovalRequest auto-blocks: REST clients cannot deliver an
// interactive decision, so any gate prompt becomes an immediate Block.
// The agent's resulting error is surfaced through the normal Error /
// Done event path.
func (c *Channel) OnApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	c.mu.Lock()
	tn := c.turns[sessionID]
	fn := c.approveFn
	c.mu.Unlock()
	if tn == nil || fn == nil {
		return
	}
	c.mu.Lock()
	tn.blocked = true
	c.mu.Unlock()
	if err := fn(sessionID, req.ID, gate.DecisionBlock, req.MatchKey); err != nil {
		log.Warn().Str("channel", "rest").Err(err).Msg("auto-block approval failed")
	}
}

// OnApprovalResolved is a no-op for REST.
func (c *Channel) OnApprovalResolved(_, _, _ string) {}

// Handler ---------------------------------------------------------------

// chatRequest is the subset of the OpenAI Chat Completions payload we
// need plus a wick-specific session_id extension. If session_id is set,
// requests with the same value reuse the same wick session (multi-turn
// — only the last user message is sent, history lives in wick). If
// omitted, every request spawns a fresh session and the full messages
// array is flattened into one prompt (stateless, pure OpenAI parity).
//
// session_id can also be supplied via metadata.session_id for clients
// that expose only the standard OpenAI fields.
type chatRequest struct {
	Model     string            `json:"model"`
	User      string            `json:"user"`
	SessionID string            `json:"session_id"`
	Metadata  map[string]string `json:"metadata"`
	Stream    bool              `json:"stream"`
	Messages  []chatMessage     `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse mirrors the OpenAI chat.completion response shape closely
// enough that off-the-shelf SDKs accept it.
type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

func (c *Channel) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !c.IsConfigured() {
		writeError(w, http.StatusServiceUnavailable, "rest channel disabled")
		return
	}
	if c.sendFn == nil {
		writeError(w, http.StatusServiceUnavailable, "rest channel not wired")
		return
	}
	if c.auth == nil {
		writeError(w, http.StatusUnauthorized, "no authenticator configured")
		return
	}

	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return
	}
	userID, err := c.auth.Authenticate(r.Context(), bearer)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	if req.Stream {
		writeError(w, http.StatusBadRequest, "streaming not supported on this endpoint")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	// Resolve session_id: explicit > metadata > "" (stateless).
	explicitSession := strings.TrimSpace(req.SessionID)
	if explicitSession == "" && req.Metadata != nil {
		explicitSession = strings.TrimSpace(req.Metadata["session_id"])
	}

	// Two modes:
	//   - stateless (no session_id): flatten the full messages array into
	//     one prompt, spawn a fresh session UUID. Client owns history.
	//   - stateful (session_id set):  send only the last user message,
	//     reuse session_id across requests so wick keeps history.
	var (
		prompt    string
		sessionID string
		reused    bool
	)
	if explicitSession == "" {
		prompt = flattenMessages(req.Messages)
		sessionID = "rest-" + uuid.NewString()
	} else {
		prompt = lastUserMessage(req.Messages)
		sessionID = "rest-" + explicitSession
		reused = true
	}
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "no user message found")
		return
	}

	c.cfgMu.Lock()
	workspace := c.cfg.Workspace
	c.cfgMu.Unlock()
	if workspace == "" {
		workspace = "main"
	}

	tn := &turn{done: make(chan struct{})}
	c.mu.Lock()
	if existing := c.turns[sessionID]; existing != nil && !existing.finished {
		c.mu.Unlock()
		writeError(w, http.StatusConflict, "session busy: a prior request is still in flight")
		return
	}
	c.turns[sessionID] = tn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.turns[sessionID] == tn {
			delete(c.turns, sessionID)
		}
		c.mu.Unlock()
	}()

	// Inject origin context only on first-ever message for this session.
	// For reused sessions whose on-disk state already exists, skip the
	// inject so we don't pollute the conversation each turn.
	if c.sessions != nil && (!reused || !c.sessions.SessionExists(sessionID)) {
		userLabel := userID
		if u := strings.TrimSpace(req.User); u != "" {
			userLabel = userID + " (" + u + ")"
		}
		ctxText := fmt.Sprintf(
			"[REST request context — sent automatically by wick]\nUser: %s\nSession: %s",
			userLabel, sessionID,
		)
		if err := c.sendFn(context.Background(), sessionID, workspace, "rest", "system", ctxText); err != nil {
			log.Warn().Str("channel", "rest").Err(err).Msg("inject session context failed")
		}
		if hook := c.onSessionStart; hook != nil {
			hook(sessionID, "rest", ctxText)
		}
	}

	if err := c.sendFn(context.Background(), sessionID, workspace, "rest", "user", prompt); err != nil {
		writeError(w, http.StatusInternalServerError, "pool dispatch failed: "+err.Error())
		return
	}

	select {
	case <-tn.done:
	case <-r.Context().Done():
		writeError(w, 499, "client closed request")
		return
	}

	c.mu.Lock()
	text := tn.buf.String()
	errMsg := tn.errMsg
	blocked := tn.blocked
	c.mu.Unlock()

	if errMsg != "" {
		status := http.StatusInternalServerError
		if blocked {
			status = http.StatusForbidden
		}
		writeError(w, status, errMsg)
		return
	}

	resp := chatResponse{
		ID:      "wick-" + sessionID + "-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   firstNonEmpty(req.Model, "wick"),
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": msg, "type": "wick_error"},
	})
}

// flattenMessages renders an OpenAI messages array into a single prompt.
// System messages are prefixed verbatim, prior assistant turns are tagged
// so the agent sees them as history, and the final user message stays at
// the bottom. Returns "" when there is no user turn.
func flattenMessages(msgs []chatMessage) string {
	hasUser := false
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			hasUser = true
			break
		}
	}
	if !hasUser {
		return ""
	}
	var b strings.Builder
	for i, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch m.Role {
		case "system":
			b.WriteString("[system] ")
			b.WriteString(content)
		case "assistant":
			b.WriteString("[assistant] ")
			b.WriteString(content)
		case "user":
			// Last user message: emit raw (no tag) so it reads as the
			// actual prompt; earlier user turns get a history tag.
			if isLastUser(msgs, i) {
				b.WriteString(content)
			} else {
				b.WriteString("[user] ")
				b.WriteString(content)
			}
		default:
			b.WriteString("[" + m.Role + "] ")
			b.WriteString(content)
		}
		if i < len(msgs)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// lastUserMessage returns the content of the most recent user message.
// Used in stateful mode where wick already owns history — only the new
// turn is sent on each request.
func lastUserMessage(msgs []chatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			s := strings.TrimSpace(msgs[i].Content)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func isLastUser(msgs []chatMessage, idx int) bool {
	for j := len(msgs) - 1; j >= 0; j-- {
		if msgs[j].Role == "user" && strings.TrimSpace(msgs[j].Content) != "" {
			return j == idx
		}
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
