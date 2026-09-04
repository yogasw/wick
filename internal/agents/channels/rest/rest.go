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
//
// Background mode: `"background": true` (or metadata.background="true")
// makes the request return immediately with status "queued" instead of
// waiting for the agent — for callers behind aggressive HTTP timeouts.
// The message is queued on the session exactly like a chat channel
// message; several background requests on one conversation stack up in
// the pool's per-session FIFO. The reply is not returned anywhere: it
// lands in the session history, so pair background with `conversation`
// and read the result with a follow-up request. Background without
// `conversation` is fire-and-forget — the output is unreachable.
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
	"github.com/yogasw/wick/internal/agents/store"
)

// Authenticator validates a plaintext Bearer token and returns the owning
// user_id. Implemented by accesstoken.Service.Authenticate.
type Authenticator interface {
	Authenticate(ctx context.Context, plain string) (userID string, err error)
}

// agentName is the pool agent every REST dispatch routes to. The project
// binding (cwd) is resolved by the pool send closure from the channel's
// configured project_id or the per-request override.
const agentName = "main"

// turn holds the state of one dispatched user message. Done is closed
// when the message's terminal event arrives. Turns queue per session in
// the same FIFO order the pool processes messages, so each waiter gets
// the reply to its own message — a turn whose waiter left (client
// disconnect) or never existed (background) stays queued until its Done
// pops it, keeping the alignment intact.
type turn struct {
	buf      strings.Builder
	done     chan struct{}
	errMsg   string
	blocked  bool
	finished bool
	// bg marks a fire-and-forget (background) turn: no waiter, the
	// buffered reply is discarded on Done — it lives in session history.
	bg bool
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

	mu sync.Mutex
	// turns holds the per-session FIFO of dispatched messages, aligned
	// with the pool's own per-session queue: head = the message the agent
	// is working on, tail = still queued. Terminal events pop the head.
	turns map[string][]*turn
	// sendMu serializes {enqueue turn + pool send} so the turns FIFO and
	// the pool's message order can never interleave differently.
	sendMu sync.Mutex

	ownerUserID string // wick user who owns this channel row; empty = App Owner
}

// New constructs a REST Channel. auth resolves the Bearer on every
// inbound request; nil disables the channel.
func New(cfg agentconfig.RestChannelConfig, auth Authenticator) *Channel {
	return &Channel{
		cfg:   cfg,
		auth:  auth,
		turns: make(map[string][]*turn),
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

// OnAgentEvent satisfies channels.AgentEventReceiver. Deltas accumulate
// on the head turn (the message the agent is working on); a terminal
// event finishes the head and pops it, advancing the queue to the next
// message. Events for sessions this channel didn't originate find an
// empty queue and are ignored.
func (c *Channel) OnAgentEvent(sessionID string, ev event.AgentEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := c.turns[sessionID]
	if len(q) == 0 {
		return
	}
	tn := q[0]
	switch ev.Type {
	case event.TextDelta:
		tn.buf.WriteString(ev.Text)
	case event.Done, event.Error:
		if !tn.finished {
			tn.finished = true
			tn.errMsg = ev.ErrorMsg
			if ev.Type == event.Error && tn.errMsg == "" {
				tn.errMsg = ev.Text
			}
			close(tn.done)
		}
		if len(q) == 1 {
			delete(c.turns, sessionID)
		} else {
			c.turns[sessionID] = q[1:]
		}
	}
}

// ApprovalReceiver -------------------------------------------------------

// OnApprovalRequest auto-blocks: REST clients cannot deliver an
// interactive decision, so any gate prompt becomes an immediate Block.
// The agent's resulting error is surfaced through the normal Error /
// Done event path.
func (c *Channel) OnApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	c.mu.Lock()
	q := c.turns[sessionID]
	fn := c.approveFn
	if len(q) > 0 {
		// The approval belongs to the active (head) turn — mark it so a
		// sync waiter surfaces the block as 403 instead of a bare error.
		q[0].blocked = true
	}
	c.mu.Unlock()
	if fn == nil || len(q) == 0 {
		return
	}
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
	// Background makes the request return immediately with status
	// "queued" instead of waiting for the agent. The reply is not
	// returned — it lands in the session history, so pair with
	// `conversation` and fetch it with a follow-up request. Also
	// accepted via metadata.background for SDKs that only expose the
	// standard OpenAI fields.
	Background bool          `json:"background"`
	Messages   []chatMessage `json:"messages"`
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
	// Status is a wick extension: "queued" on background requests, empty
	// (omitted) on the normal synchronous path. OpenAI SDKs ignore it.
	Status string `json:"status,omitempty"`
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

	if resolveBackground(req.Background, req.Metadata) {
		if status, msg := c.dispatchBackground(sessionID, userID, req.User, prompt, reused, resolveProject(req.Project, req.Metadata)); status != 0 {
			writeError(w, status, msg)
			return
		}
		resp := chatResponse{
			ID:      "wick-" + sessionID + "-" + fmt.Sprintf("%d", time.Now().UnixNano()),
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   firstNonEmpty(req.Model, "wick"),
			Status:  "queued",
			Choices: []chatChoice{{
				Index:        0,
				Message:      chatMessage{Role: "assistant", Content: ""},
				FinishReason: "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
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

// resolveBackground picks the background flag from the explicit field or,
// failing that, metadata.background — for SDKs that only expose the
// standard OpenAI `metadata` map.
func resolveBackground(background bool, metadata map[string]string) bool {
	if background {
		return true
	}
	if metadata != nil {
		switch strings.ToLower(strings.TrimSpace(metadata["background"])) {
		case "true", "1", "yes":
			return true
		}
	}
	return false
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

// dispatch enqueues the prompt on the session's turn FIFO, sends it to
// the agent pool, and waits for that message's Done. A busy session no
// longer 409s — the request queues behind the in-flight turns exactly
// like chat messages, and each waiter receives the reply to its own
// message. Returns either a result (status 0) or an HTTP error.
func (c *Channel) dispatch(ctx context.Context, sessionID, userID, userField, prompt string, reused bool, projectOverride string) (dispatchResult, int, string) {
	sendCtx := c.newSendCtx(projectOverride)

	tn := &turn{done: make(chan struct{})}
	if err := c.enqueueAndSend(sendCtx, tn, sessionID, userID, userField, prompt, reused); err != nil {
		return dispatchResult{}, http.StatusInternalServerError, "pool dispatch failed: " + err.Error()
	}

	select {
	case <-tn.done:
	case <-ctx.Done():
		// Client gave up (timeout / stop button). The turn deliberately
		// stays queued: its terminal event must still pop it, otherwise
		// every later reply on this session shifts one message back.
		return dispatchResult{}, 499, "client closed request"
	}

	c.mu.Lock()
	res := dispatchResult{text: tn.buf.String(), errMsg: tn.errMsg, blocked: tn.blocked}
	c.mu.Unlock()
	return res, 0, ""
}

// dispatchBackground queues the prompt and returns without waiting for
// the agent — the REST analogue of dropping a message into a chat
// channel. The bg turn keeps the FIFO aligned with the pool's queue and
// keeps approval auto-block alive; its buffered reply is discarded on
// Done (the output lives in the session history).
func (c *Channel) dispatchBackground(sessionID, userID, userField, prompt string, reused bool, projectOverride string) (int, string) {
	sendCtx := c.newSendCtx(projectOverride)
	tn := &turn{done: make(chan struct{}), bg: true}
	if err := c.enqueueAndSend(sendCtx, tn, sessionID, userID, userField, prompt, reused); err != nil {
		return http.StatusInternalServerError, "pool dispatch failed: " + err.Error()
	}
	return 0, ""
}

// enqueueAndSend appends tn to the session's turn FIFO and dispatches the
// prompt (plus the one-time origin-context inject) to the pool. sendMu
// makes {enqueue + send} atomic across requests, so the channel's FIFO
// can never order differently from the pool's per-session queue. On send
// failure the turn is removed again — it never reached the pool.
func (c *Channel) enqueueAndSend(sendCtx context.Context, tn *turn, sessionID, userID, userField, prompt string, reused bool) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.maybeInjectContext(sendCtx, sessionID, userID, userField, reused)

	c.mu.Lock()
	c.turns[sessionID] = append(c.turns[sessionID], tn)
	c.mu.Unlock()

	if err := c.sendUserTurn(sendCtx, sessionID, userID, userField, prompt); err != nil {
		c.mu.Lock()
		q := c.turns[sessionID]
		for i, t := range q {
			if t == tn {
				c.turns[sessionID] = append(q[:i:i], q[i+1:]...)
				break
			}
		}
		if len(c.turns[sessionID]) == 0 {
			delete(c.turns, sessionID)
		}
		c.mu.Unlock()
		return err
	}
	return nil
}

// newSendCtx builds the context every pool dispatch uses. It is detached
// from the HTTP request (the pool spawns the CLI with this ctx;
// inheriting the request ctx would kill it on return) but carries both
// project signals: this instance's configured default, and the
// per-request override that outranks it.
func (c *Channel) newSendCtx(projectOverride string) context.Context {
	c.cfgMu.Lock()
	instanceProject := c.cfg.ProjectID
	c.cfgMu.Unlock()
	sendCtx := agentchannels.WithChannelProject(context.Background(), instanceProject)
	return agentchannels.WithProjectOverride(sendCtx, projectOverride)
}

// maybeInjectContext sends the one-time origin-context system turn when
// the session is brand new (or stateless). Best-effort — a failed inject
// never blocks the user message.
func (c *Channel) maybeInjectContext(sendCtx context.Context, sessionID, userID, userField string, reused bool) {
	if c.sessions == nil || (reused && c.sessions.SessionExists(sessionID)) {
		return
	}
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

// sendUserTurn dispatches the prompt as the authenticated user. userID is
// the wick account behind the Bearer token — the trustworthy half.
// userField is a free-text label the client chose (an end-user name in a
// bot relaying for others), so it is a display name only and never the
// identity: a caller can write anything there, but cannot change whose
// token this is.
func (c *Channel) sendUserTurn(sendCtx context.Context, sessionID, userID, userField, prompt string) error {
	sender := &store.Sender{
		ID:         userID,
		Name:       strings.TrimSpace(userField),
		Channel:    "rest",
		WickUserID: userID,
	}
	return c.sendFn(agentchannels.WithSender(sendCtx, sender), sessionID, agentName, "rest", "user", prompt)
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
