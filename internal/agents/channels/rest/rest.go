// Package rest implements an OpenAI Chat Completions compatible HTTP
// channel for the agents pool. Clients use any OpenAI SDK pointed at
// http://<wick>/integrations/rest/api/v1/openai with a wick Personal Access Token
// as the Bearer.
//
// Request shape (subset of OpenAI):
//
//	POST /integrations/rest/api/v1/openai/chat/completions
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
	"crypto/sha256"
	"encoding/hex"
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

	ownerUserID string // wick user who owns this channel row; empty = App Owner
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

// NewWithOwner creates a REST Channel tied to a specific wick user owner.
// ownerUserID="" means the App Owner's channel (user_id = NULL row).
func NewWithOwner(cfg agentconfig.RestChannelConfig, auth Authenticator, ownerUserID string) *Channel {
	ch := New(cfg, auth)
	ch.ownerUserID = ownerUserID
	return ch
}

// Name satisfies Channel.
func (c *Channel) Name() string { return "rest" }

// Auth returns the Authenticator wired into this channel. Used by the
// live-sync path to mint a new keyed instance reusing the boot-time auth.
func (c *Channel) Auth() Authenticator { return c.auth }

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
	log.Info().Str("channel", "rest").Msg("started — POST /integrations/rest/api/v1/openai/chat/completions")
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

// HTTPHandlers satisfies channels.MultiHTTPHandlerProvider.
// Mounts three OpenAI-compatible routes under /integrations/rest/api/v1/openai, so
// any OpenAI SDK pointed at that base URL works without extra config:
//
//   - POST /chat/completions — Chat Completions API
//   - POST /responses        — Responses API (with previous_response_id chaining)
//   - GET  /models           — list of advertised models
func (c *Channel) HTTPHandlers() map[string]http.Handler {
	return map[string]http.Handler{
		"POST /integrations/rest/api/v1/openai/chat/completions": http.HandlerFunc(c.handleChatCompletions),
		"POST /integrations/rest/api/v1/openai/responses":        http.HandlerFunc(c.handleResponses),
		"GET /integrations/rest/api/v1/openai/models":            http.HandlerFunc(c.handleModels),
	}
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
// need plus a `conversation` extension borrowed from OpenAI's Responses
// API. When conversation is set, requests with the same value reuse one
// wick session (multi-turn — only the last user message is sent, history
// lives in wick). When omitted, every request spawns a fresh session and
// the full messages array is flattened into one prompt (stateless, pure
// OpenAI parity). conversation may also be supplied via metadata for
// clients that expose only the standard OpenAI fields.
type chatRequest struct {
	Model        string            `json:"model"`
	User         string            `json:"user"`
	Conversation string            `json:"conversation"`
	Metadata     map[string]string `json:"metadata"`
	Stream       bool              `json:"stream"`
	Messages     []chatMessage     `json:"messages"`
	// Project optionally names the wick Project (id) for this request,
	// overriding the channel's configured default. Also accepted via
	// metadata.project / metadata.project_id for SDKs that only expose
	// the standard OpenAI `metadata` map.
	Project string `json:"project"`
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
	if status, msg := c.checkReady(); status != 0 {
		writeError(w, status, msg)
		return
	}
	userID, status, msg := c.authBearer(r)
	if status != 0 {
		writeError(w, status, msg)
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
	if !IsModelAllowed(req.Model) {
		writeModelNotFound(w, req.Model)
		return
	}

	explicitSession := resolveConversation(req.Conversation, req.Metadata)

	// Two modes:
	//   - stateless (no conversation): flatten the full messages array
	//     into one prompt, spawn a fresh session UUID. Client owns history.
	//   - stateful (conversation set): send only the last user message,
	//     reuse the same wick session across requests so wick keeps history.
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
		// Namespace the client-chosen conversation key by the authenticated
		// user so two callers (different tokens/owners) reusing the same
		// conversation string never collide on one pool session.
		sessionID = restSessionID(userID, explicitSession)
		reused = true
	}
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "no user message found")
		return
	}

	res, status, msg := c.dispatch(r.Context(), sessionID, userID, req.User, prompt, reused, resolveProject(req.Project, req.Metadata))
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if res.errMsg != "" {
		s := http.StatusInternalServerError
		if res.blocked {
			s = http.StatusForbidden
		}
		writeError(w, s, res.errMsg)
		return
	}

	resp := chatResponse{
		ID:      "wick-" + sessionID + "-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   firstNonEmpty(req.Model, "wick"),
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatMessage{Role: "assistant", Content: res.text},
			FinishReason: "stop",
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// restSessionID builds the internal wick session id for a client-chosen
// conversation key, namespaced by the authenticated user so two callers
// (different PATs/owners) reusing the same conversation string never
// collide on one pool session.
//
// Form: "rest-<scope>-<key>" where scope is the first 8 hex chars of
// sha256(userID). The "<scope>-<key>" tail round-trips opaquely in the
// client-facing id (resp_<tail> / chat completion id), so previous_response_id
// chaining and conversation reuse keep working for the same authenticated
// user without leaking the raw user id. Empty userID → no scope, preserving
// the legacy single-owner id form ("rest-<key>").
func restSessionID(userID, key string) string {
	if strings.TrimSpace(userID) == "" {
		return "rest-" + key
	}
	sum := sha256.Sum256([]byte(userID))
	scope := hex.EncodeToString(sum[:])[:8]
	return "rest-" + scope + "-" + key
}

// resolveConversation picks the conversation key from the explicit
// field or, failing that, metadata.conversation. Empty result means
// stateless (handler spawns a fresh session UUID). The name mirrors
// OpenAI's Responses API field so wick speaks one vocabulary across
// both endpoints.
func resolveConversation(conversation string, metadata map[string]string) string {
	if v := strings.TrimSpace(conversation); v != "" {
		return v
	}
	if metadata != nil {
		if v := strings.TrimSpace(metadata["conversation"]); v != "" {
			return v
		}
	}
	return ""
}

// resolveProject picks the per-request project id from the explicit
// `project` field or, failing that, metadata.project / metadata.project_id.
// Empty means "use the channel's configured default project".
func resolveProject(project string, metadata map[string]string) string {
	if v := strings.TrimSpace(project); v != "" {
		return v
	}
	if metadata != nil {
		if v := strings.TrimSpace(metadata["project"]); v != "" {
			return v
		}
		if v := strings.TrimSpace(metadata["project_id"]); v != "" {
			return v
		}
	}
	return ""
}

// checkReady returns a non-zero (status, msg) when the channel cannot
// serve requests (disabled, not wired, no auth). status 0 means OK.
func (c *Channel) checkReady() (int, string) {
	if !c.IsConfigured() {
		return http.StatusServiceUnavailable, "rest channel disabled"
	}
	if c.sendFn == nil {
		return http.StatusServiceUnavailable, "rest channel not wired"
	}
	if c.auth == nil {
		return http.StatusUnauthorized, "no authenticator configured"
	}
	return 0, ""
}

// authBearer extracts and validates the Bearer token. Returns the owning
// user_id on success; otherwise an HTTP status + message.
func (c *Channel) authBearer(r *http.Request) (string, int, string) {
	bearer := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if bearer == "" {
		return "", http.StatusUnauthorized, "missing bearer token"
	}
	uid, err := c.auth.Authenticate(r.Context(), bearer)
	if err != nil {
		return "", http.StatusUnauthorized, "invalid token"
	}
	return uid, 0, ""
}

// dispatchResult carries the agent's terminal state for one request.
type dispatchResult struct {
	text    string
	errMsg  string
	blocked bool
}

// dispatch claims sessionID, optionally injects origin context, sends the
// prompt to the agent pool, and waits for Done. Returns either a result
// (status 0) or an HTTP error (non-zero status + msg).
func (c *Channel) dispatch(ctx context.Context, sessionID, userID, userField, prompt string, reused bool, projectOverride string) (dispatchResult, int, string) {
	// agentName is the pool agent to route to; default "main". The
	// project binding (cwd) is resolved by the pool send closure from
	// the channel's configured project_id — unless the request named a
	// project, in which case projectOverride wins (threaded via ctx).
	agentName := "main"
	// sendCtx is detached from the HTTP request (the pool spawns the CLI
	// with this ctx; inheriting the request ctx would kill it on return)
	// but still carries the per-request project override.
	sendCtx := agentchannels.WithProjectOverride(context.Background(), projectOverride)

	tn := &turn{done: make(chan struct{})}
	c.mu.Lock()
	if existing := c.turns[sessionID]; existing != nil && !existing.finished {
		c.mu.Unlock()
		return dispatchResult{}, http.StatusConflict, "session busy: a prior request is still in flight"
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

	if c.sessions != nil && (!reused || !c.sessions.SessionExists(sessionID)) {
		userLabel := userID
		if u := strings.TrimSpace(userField); u != "" {
			userLabel = userID + " (" + u + ")"
		}
		ctxText := fmt.Sprintf(
			"[REST request context — sent automatically by wick]\nUser: %s\nSession: %s",
			userLabel, sessionID,
		)
		if err := c.sendFn(sendCtx, sessionID, agentName, "rest", "system", ctxText); err != nil {
			log.Warn().Str("channel", "rest").Err(err).Msg("inject session context failed")
		}
		if hook := c.onSessionStart; hook != nil {
			hook(sessionID, "rest", ctxText)
		}
	}

	if err := c.sendFn(sendCtx, sessionID, agentName, "rest", "user", prompt); err != nil {
		return dispatchResult{}, http.StatusInternalServerError, "pool dispatch failed: " + err.Error()
	}

	select {
	case <-tn.done:
	case <-ctx.Done():
		return dispatchResult{}, 499, "client closed request"
	}

	c.mu.Lock()
	res := dispatchResult{text: tn.buf.String(), errMsg: tn.errMsg, blocked: tn.blocked}
	c.mu.Unlock()
	return res, 0, ""
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
