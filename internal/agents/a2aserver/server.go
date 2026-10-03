// Package a2aserver exposes Team agents as Agent-to-Agent (A2A) servers.
//
// Each agent with an enabled connection answers JSON-RPC on
// /integrations/a2a/<agent_id> and publishes its agent card under that
// path's /.well-known/agent-card.json. A message is a turn of the agent in
// a session of its own — one per A2A contextId — dispatched through the
// same pool closure every other channel uses, so the agent's persona,
// checklist and gates apply exactly as they do on the web.
package a2aserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
)

// Source is the channel name, the dispatch source and so the origin of
// every session this server creates.
const Source = "a2a"

// agentName is the pool agent every dispatch routes to.
const agentName = "main"

// Directory resolves agents and their sessions. Implemented by the Team
// tool, which owns both.
type Directory interface {
	// Agent returns the agent; ok=false when it does not exist.
	Agent(ctx context.Context, id string) (Agent, bool)
	// EnsureSession creates sessionID as a session of a when it does not
	// exist yet. created reports whether it did.
	EnsureSession(ctx context.Context, a Agent, sessionID string) (created bool, err error)
}

// TokenAuthenticator resolves a wick Personal Access Token.
type TokenAuthenticator interface {
	AuthenticateToken(ctx context.Context, plain string) (*entity.PersonalAccessToken, error)
}

// Caller is who an A2A request authenticated as.
type Caller struct {
	// ID is the sender id on the turn: the PAT's user, or a stand-in for
	// the connection key, which belongs to no wick user.
	ID         string
	Name       string
	WickUserID string
	Token      agentchannels.CallerToken
}

// Server is the A2A channel. Start/Stop are no-ops: its routes are mounted
// by the registry and live for the server's lifetime.
type Server struct {
	dir       Directory
	conns     ConnStore
	pat       TokenAuthenticator
	publicURL func() string

	sendFn    agentchannels.SendFunc
	approveFn agentchannels.ApproveFn

	mu sync.Mutex
	// turns is the per-session FIFO of dispatched messages, aligned with
	// the pool's queue (see the REST channel, which this mirrors).
	turns  map[string][]*turn
	sendMu sync.Mutex

	hmu      sync.Mutex
	handlers map[string]http.Handler // agent id → JSON-RPC handler
	mux      *http.ServeMux
}

// New builds the server. pat may be nil (keys only); publicURL is read on
// every card so an app URL edit applies without a restart.
func New(dir Directory, conns ConnStore, pat TokenAuthenticator, publicURL func() string) *Server {
	s := &Server{
		dir: dir, conns: conns, pat: pat, publicURL: publicURL,
		turns:    map[string][]*turn{},
		handlers: map[string]http.Handler{},
		mux:      http.NewServeMux(),
	}
	for p, h := range s.HTTPHandlers() {
		s.mux.Handle(p, h)
	}
	return s
}

// Name satisfies channels.Channel.
func (s *Server) Name() string { return Source }

// Start satisfies channels.Channel.
func (s *Server) Start(context.Context) error { return nil }

// Stop satisfies channels.Channel.
func (s *Server) Stop() {}

// IsConfigured satisfies channels.Channel: per-agent connections decide.
func (s *Server) IsConfigured() bool { return true }

// SetSendFunc receives the pool dispatch closure.
func (s *Server) SetSendFunc(fn agentchannels.SendFunc) { s.sendFn = fn }

// SetApproveFn receives the gate approval resolver.
func (s *Server) SetApproveFn(fn agentchannels.ApproveFn) { s.approveFn = fn }

// HTTPHandlers mounts the endpoint and the agent card.
func (s *Server) HTTPHandlers() map[string]http.Handler {
	return map[string]http.Handler{
		"POST /integrations/a2a/{agent_id}":                             http.HandlerFunc(s.serveRPC),
		"GET /integrations/a2a/{agent_id}/.well-known/agent-card.json": http.HandlerFunc(s.serveCard),
	}
}

// SessionID is the wick session of an A2A contextId: one per (agent,
// context). The context id is the caller's string, so it is hashed into
// the id rather than trusted as a path segment.
func SessionID(agentID, contextID string) string {
	sum := sha256.Sum256([]byte(contextID))
	return "a2a-" + agentID + "-" + hex.EncodeToString(sum[:8])
}

// ── HTTP ────────────────────────────────────────────────────────────

// resolve finds the agent and its enabled connection, answering the
// request itself when there is none. A connection left behind by a deleted
// agent is dropped on the way.
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) (Agent, Connection, bool) {
	id := r.PathValue("agent_id")
	a, ok := s.dir.Agent(r.Context(), id)
	if !ok {
		if _, found, _ := s.conns.Load(id); found {
			if err := s.conns.Delete(id); err != nil {
				log.Warn().Err(err).Str("agent", id).Msg("a2a: drop connection of a deleted agent")
			}
		}
		writeRPCError(w, http.StatusNotFound, "agent not found")
		return Agent{}, Connection{}, false
	}
	c, found, err := s.conns.Load(id)
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, "connection lookup failed")
		return Agent{}, Connection{}, false
	}
	if !found || !c.Enabled {
		writeRPCError(w, http.StatusNotFound, "agent not found")
		return Agent{}, Connection{}, false
	}
	if a.Disabled {
		writeRPCError(w, http.StatusGone, "agent is disabled")
		return Agent{}, Connection{}, false
	}
	return a, c, true
}

// authenticate checks the Bearer: the connection key, else a PAT of the
// owner or of an allowed caller. The connection test is let through by
// its own context flag, which no network request can carry.
func (s *Server) authenticate(r *http.Request, a Agent, c Connection) (Caller, bool) {
	if isProbe(r.Context()) {
		return Caller{ID: "a2a-test", Name: "Connection test"}, true
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return Caller{}, false
	}
	bearer := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	if bearer == "" {
		return Caller{}, false
	}
	if VerifyKey(bearer, c.KeyHash) {
		return Caller{ID: "a2a-key-" + c.KeyHint, Name: "A2A key …" + c.KeyHint}, true
	}
	if s.pat == nil || strings.HasPrefix(bearer, keyPrefix) {
		return Caller{}, false
	}
	row, err := s.pat.AuthenticateToken(r.Context(), bearer)
	if err != nil || row == nil {
		return Caller{}, false
	}
	if row.UserID != a.OwnerUserID && !slices.Contains(c.AllowedCallers, row.UserID) {
		return Caller{}, false
	}
	return Caller{
		ID: row.UserID, Name: row.Name, WickUserID: row.UserID,
		Token: agentchannels.CallerToken{ID: row.ID, Name: row.Name},
	}, true
}

func (s *Server) serveRPC(w http.ResponseWriter, r *http.Request) {
	a, c, ok := s.resolve(w, r)
	if !ok {
		return
	}
	cl, ok := s.authenticate(r, a, c)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="wick-a2a"`)
		writeRPCError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.handlerFor(a.ID).ServeHTTP(w, r.WithContext(withCaller(r.Context(), cl)))
}

func (s *Server) serveCard(w http.ResponseWriter, r *http.Request) {
	a, c, ok := s.resolve(w, r)
	if !ok {
		return
	}
	if !c.PublicCard {
		if _, ok := s.authenticate(r, a, c); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="wick-a2a"`)
			writeRPCError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(BuildCard(a, s.baseURL()))
}

func (s *Server) baseURL() string {
	if s.publicURL == nil {
		return ""
	}
	return strings.TrimRight(s.publicURL(), "/")
}

// handlerFor returns agentID's JSON-RPC handler. One per agent, kept, so a
// task stays readable (tasks/get) after the request that ran it.
func (s *Server) handlerFor(agentID string) http.Handler {
	s.hmu.Lock()
	defer s.hmu.Unlock()
	if h, ok := s.handlers[agentID]; ok {
		return h
	}
	rh := a2asrv.NewHandler(&executor{s: s, agentID: agentID}, a2asrv.WithCallInterceptors(callerInterceptor{}))
	h := a2asrv.NewJSONRPCHandler(rh)
	s.handlers[agentID] = h
	return h
}

func writeRPCError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": nil,
		"error": map[string]any{"code": -32000, "message": msg},
	})
}

// ── caller plumbing ─────────────────────────────────────────────────

type callerKey struct{}
type probeKey struct{}

func withCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func withProbe(ctx context.Context) context.Context { return context.WithValue(ctx, probeKey{}, true) }

func isProbe(ctx context.Context) bool { v, _ := ctx.Value(probeKey{}).(bool); return v }

// callerInterceptor hands the authenticated caller to the executor as the
// call's user: the SDK copies it onto every ExecutorContext, whichever
// goroutine runs the execution.
type callerInterceptor struct{ a2asrv.PassthroughCallInterceptor }

func (callerInterceptor) Before(ctx context.Context, cc *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	if c, ok := ctx.Value(callerKey{}).(Caller); ok {
		cc.User = a2asrv.NewAuthenticatedUser(c.ID, map[string]any{
			"name": c.Name, "wick_user_id": c.WickUserID, "token_id": c.Token.ID, "token_name": c.Token.Name,
		})
	}
	return ctx, nil, nil
}

func callerOf(u *a2asrv.User) Caller {
	if u == nil {
		return Caller{}
	}
	str := func(k string) string { v, _ := u.Attributes[k].(string); return v }
	return Caller{
		ID: u.Name, Name: str("name"), WickUserID: str("wick_user_id"),
		Token: agentchannels.CallerToken{ID: str("token_id"), Name: str("token_name")},
	}
}

// ── pool bridge ─────────────────────────────────────────────────────

// turn is one dispatched message. Events are buffered, never blocking the
// registry fan-out; the executor drains them when woken.
type turn struct {
	mu   sync.Mutex
	evs  []event.AgentEvent
	wake chan struct{}
}

func newTurn() *turn { return &turn{wake: make(chan struct{}, 1)} }

func (t *turn) push(ev event.AgentEvent) {
	t.mu.Lock()
	t.evs = append(t.evs, ev)
	t.mu.Unlock()
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

func (t *turn) drain() []event.AgentEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.evs
	t.evs = nil
	return out
}

// OnAgentEvent satisfies channels.AgentEventReceiver: text and terminal
// events go to the session's head turn; a terminal one pops it. Sessions
// this server did not dispatch find no queue.
func (s *Server) OnAgentEvent(sessionID string, ev event.AgentEvent) {
	if ev.Type != event.TextDelta && ev.Type != event.Done && ev.Type != event.Error {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.turns[sessionID]
	if len(q) == 0 {
		return
	}
	q[0].push(ev)
	if ev.Type == event.Done || ev.Type == event.Error {
		if len(q) == 1 {
			delete(s.turns, sessionID)
		} else {
			s.turns[sessionID] = q[1:]
		}
	}
}

// OnApprovalRequest auto-blocks, as REST does: an A2A caller has no way to
// press an approval button, and an approval nobody can answer would hold
// the turn until it times out.
func (s *Server) OnApprovalRequest(sessionID string, req gate.ApprovalRequest) {
	s.mu.Lock()
	mine := len(s.turns[sessionID]) > 0
	fn := s.approveFn
	s.mu.Unlock()
	if !mine || fn == nil {
		return
	}
	if err := fn(sessionID, req.ID, gate.DecisionBlock, req.MatchKey); err != nil {
		log.Warn().Str("channel", Source).Err(err).Msg("a2a: auto-block approval failed")
	}
}

// OnApprovalResolved satisfies channels.ApprovalReceiver.
func (s *Server) OnApprovalResolved(_, _, _ string) {}

// dispatch queues text as the agent's next turn in sessionID and returns
// the turn its events arrive on.
//
// The turn runs as the agent's owner — the caller id on the context is
// the owner, whoever authenticated — so a connection can never reach
// further than the owner's own chat with the agent. The real caller is
// recorded as the sender, for the transcript and the sender chip.
func (s *Server) dispatch(a Agent, sessionID string, cl Caller, contextID, text string) (*turn, error) {
	if s.sendFn == nil {
		return nil, fmt.Errorf("a2a: no pool dispatch wired")
	}
	created, err := s.dir.EnsureSession(context.Background(), a, sessionID)
	if err != nil {
		return nil, fmt.Errorf("a2a: create session: %w", err)
	}
	ctx := agentchannels.WithChannelProject(context.Background(), a.ProjectID)
	ctx = agentchannels.WithProjectOverride(ctx, a.ProjectID)
	ctx = agentchannels.WithCallerUserID(ctx, a.OwnerUserID)
	ctx = agentchannels.WithCallerToken(ctx, cl.Token)

	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if created {
		note := fmt.Sprintf("[A2A request context — sent automatically by wick]\nCaller: %s\nA2A context: %s\nSession: %s",
			firstNonEmpty(cl.Name, cl.ID), contextID, sessionID)
		if err := s.sendFn(ctx, sessionID, agentName, Source, "system", note); err != nil {
			log.Warn().Str("channel", Source).Err(err).Msg("a2a: inject session context failed")
		}
	}
	tn := newTurn()
	s.mu.Lock()
	s.turns[sessionID] = append(s.turns[sessionID], tn)
	s.mu.Unlock()
	sender := &store.Sender{ID: cl.ID, Name: firstNonEmpty(cl.Name, cl.ID), Channel: Source, WickUserID: cl.WickUserID}
	if err := s.sendFn(agentchannels.WithSender(ctx, sender), sessionID, agentName, Source, "user", text); err != nil {
		s.mu.Lock()
		q := s.turns[sessionID]
		if i := slices.Index(q, tn); i >= 0 {
			s.turns[sessionID] = slices.Delete(q, i, i+1)
		}
		if len(s.turns[sessionID]) == 0 {
			delete(s.turns, sessionID)
		}
		s.mu.Unlock()
		return nil, err
	}
	return tn, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
