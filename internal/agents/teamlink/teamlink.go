// Package teamlink lets one Team agent talk to another over A2A.
//
// The wire model is the a2a-go SDK's own (a2a.Message, a2a.Task,
// a2a.AgentCard), not a lookalike, so the same three layers serve a local
// teammate today and a remote A2A agent later:
//
//   - executor.go  — an a2asrv.AgentExecutor per Team agent: one inbound
//     message becomes one turn of that agent's main conversation, run as
//     the agent with its own persona and access.
//   - transport.go — the "wick-local" transport: an a2aclient.Transport
//     that calls the agent's a2asrv.RequestHandler as a Go function. No
//     server, no port, no HTTP.
//   - teamlink.go  — the registry (handle → AgentCard), the one client
//     every caller goes through, and the anti-loop budget.
//
// A remote agent (fase 2a′) only needs its card from /.well-known; the
// client factory then picks the JSON-RPC/REST transport from that card and
// nothing above this package changes.
package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/google/uuid"
)

// Limits on agent-to-agent traffic. A2A→B→A counts as three turns of the
// same context, so two agents can go back and forth twice before one of
// them has to stop and report.
const (
	// MaxContextTurns caps the messages exchanged inside one context_id.
	MaxContextTurns = 4
	// MaxDepth caps a chain of agents each waiting on the next.
	MaxDepth = 3
	// DefaultWait is how long a synchronous call waits for the reply.
	DefaultWait = 90 * time.Second
	// MaxWait clamps a caller-supplied wait.
	MaxWait = 300 * time.Second
)

// Metadata keys carried on every message. They are set by the Hub from
// the calling SESSION, never from model input.
const (
	metaFrom    = "wick.from_agent"
	metaDepth   = "wick.depth"
	metaSession = "wick.caller_session"
)

var (
	// ErrHopLimit ends a chain that has gone on long enough. Worded for
	// the model reading it.
	ErrHopLimit = errors.New("hop limit reached — summarise and report to the user")
	// ErrUnknownHandle means no enabled agent of the owner has the handle.
	ErrUnknownHandle = errors.New("unknown or disabled Team handle")
	// ErrSelf refuses a message to the calling agent itself.
	ErrSelf = errors.New("you can't message yourself — pick a teammate from your Team")
	// ErrNotTeamSession refuses a caller that is not a Team agent.
	ErrNotTeamSession = errors.New("team messaging is only available in a Team agent's session")
	// ErrUnknownTask means the task id was never issued to this caller.
	ErrUnknownTask = errors.New("unknown task id")
)

// Peer is one Team agent as the registry sees it.
type Peer struct {
	ID, OwnerID, Handle        string
	Name, Tagline, Description string
	IsCaptain, Disabled        bool
}

// Label is how a peer is named in framing and replies.
func (p Peer) Label() string {
	name := p.Name
	if name == "" {
		name = p.Handle
	}
	return fmt.Sprintf("%s (@%s)", name, p.Handle)
}

// Directory reads Team agents. Implemented over team.Service in wiring.
type Directory interface {
	// Peers lists every agent ownerID owns, disabled ones included.
	Peers(ctx context.Context, ownerID string) ([]Peer, error)
	// Get returns one agent by id.
	Get(ctx context.Context, agentID string) (Peer, error)
}

// Turns runs one turn of an agent's main conversation. Implemented over
// the pool in wiring: it queues behind whatever that conversation is
// already doing, runs as the agent (its persona, scope and run_as) and
// returns the final text of the turn.
type Turns interface {
	Run(ctx context.Context, agent Peer, text string) (sessionID, reply string, err error)
}

// Notifier hands results and audit lines back to sessions.
type Notifier interface {
	// Deliver wakes sessionID with a late reply, the way an async
	// sub-agent result is delivered.
	Deliver(ctx context.Context, sessionID, text string) error
	// Audit records a mention_handoff event in sessionID's thread.
	Audit(ctx context.Context, sessionID string, h Handoff)
}

// Handoff is one mention_handoff audit event.
type Handoff struct {
	From, To, ContextID, TaskID string
	State                       a2a.TaskState
}

// Hub is the registry, the client and the bookkeeping, in one place so
// every entry point (team_message, @mention routing) shares one budget.
type Hub struct {
	Dir    Directory
	Turns  Turns
	Notify Notifier

	// poll is how often a waiting call re-reads its task. Tests shorten it.
	poll time.Duration

	mu       sync.Mutex
	handlers map[string]a2asrv.RequestHandler
	tasks    map[a2a.TaskID]*taskRef
	contexts map[string]int
	inflight map[string]inbound
	// last is the inbound task each agent answered most recently. A
	// mention in that answer continues it, so "@a thanks" → "@b thanks"
	// stays inside one context's turn budget instead of starting fresh.
	last map[string]inbound
}

// taskRef is what the Hub remembers about a task it sent.
type taskRef struct {
	agentID       string
	callerAgentID string
	callerSession string
	to            Peer
	// waiterGone: the sending call returned before the task finished, so
	// the result must be delivered back. finished/delivered close the race
	// between that return and the executor finishing.
	waiterGone, finished, delivered bool
	reply                           string
	state                           a2a.TaskState
}

// inbound is the task an agent is currently answering. A message the
// agent sends during that turn continues its context, one level deeper.
type inbound struct {
	contextID string
	depth     int
}

// NewHub returns an empty Hub.
func NewHub(dir Directory, turns Turns, notify Notifier) *Hub {
	return &Hub{
		Dir: dir, Turns: turns, Notify: notify,
		poll:     250 * time.Millisecond,
		handlers: map[string]a2asrv.RequestHandler{},
		tasks:    map[a2a.TaskID]*taskRef{},
		contexts: map[string]int{},
		inflight: map[string]inbound{},
		last:     map[string]inbound{},
	}
}

// NormalizeHandle trims a typed handle the way team.NormalizeHandle does.
func NormalizeHandle(h string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "@"))
}

// Reachable lists the enabled teammates of agentID, itself excluded. The
// tool is offered only when this is non-empty.
func (h *Hub) Reachable(ctx context.Context, agentID string) ([]Peer, error) {
	self, err := h.Dir.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	all, err := h.Dir.Peers(ctx, self.OwnerID)
	if err != nil {
		return nil, err
	}
	out := make([]Peer, 0, len(all))
	for _, p := range all {
		if p.ID != self.ID && !p.Disabled && p.OwnerID == self.OwnerID {
			out = append(out, p)
		}
	}
	return out, nil
}

// Resolve finds ownerID's enabled agent called handle and returns its
// card. Only that owner's agents are ever looked at.
func (h *Hub) Resolve(ctx context.Context, ownerID, handle string) (*a2a.AgentCard, Peer, error) {
	handle = NormalizeHandle(handle)
	all, err := h.Dir.Peers(ctx, ownerID)
	if err != nil {
		return nil, Peer{}, err
	}
	for _, p := range all {
		if p.Handle == handle && p.OwnerID == ownerID && !p.Disabled {
			return Card(p), p, nil
		}
	}
	return nil, Peer{}, fmt.Errorf("%w: @%s", ErrUnknownHandle, handle)
}

// Card builds a Team agent's AgentCard. It advertises the wick-local
// interface only; a public A2A endpoint would add its own.
func Card(p Peer) *a2a.AgentCard {
	desc := strings.TrimSpace(p.Description)
	if t := strings.TrimSpace(p.Tagline); t != "" {
		desc = strings.TrimSpace(t + ". " + desc)
	}
	name := p.Name
	if name == "" {
		name = p.Handle
	}
	return &a2a.AgentCard{
		Name:        name,
		Description: desc,
		Version:     "1",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL:             LocalURL(p.ID),
			ProtocolBinding: Protocol,
			ProtocolVersion: a2a.Version,
		}},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}
}

// client builds the A2A client for card. Every caller goes through here,
// so the transport is picked from the card, never hard-wired.
func (h *Hub) client(ctx context.Context, card *a2a.AgentCard) (*a2aclient.Client, error) {
	return a2aclient.NewFromCard(ctx, card,
		a2aclient.WithTransport(Protocol, a2aclient.TransportFactoryFn(h.createTransport)))
}

// SendInput is one message from a Team agent's session.
type SendInput struct {
	// CallerSession and CallerAgentID are resolved from the session by
	// the caller of Send, never taken from model input.
	CallerSession string
	CallerAgentID string
	To            string
	Text          string
	ContextID     string
	// Wait bounds the synchronous part. 0 = DefaultWait; negative = do
	// not wait at all (the @mention router).
	Wait time.Duration
	// Mention marks an @handle line in an agent's own reply: it continues
	// the exchange that reply answered. A person's mention (Human) always
	// starts a fresh exchange.
	Mention, Human bool
}

// Result is what a caller gets back.
type Result struct {
	TaskID    string `json:"task_id"`
	ContextID string `json:"context_id"`
	State     string `json:"state"`
	To        string `json:"to"`
	ReplyText string `json:"reply_text,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Send delivers in.Text to the teammate named in.To and waits up to
// in.Wait for the reply. A reply that is not ready in time is delivered
// back into in.CallerSession when it lands.
func (h *Hub) Send(ctx context.Context, in SendInput) (*Result, error) {
	if in.CallerAgentID == "" {
		return nil, ErrNotTeamSession
	}
	caller, err := h.Dir.Get(ctx, in.CallerAgentID)
	if err != nil {
		return nil, ErrNotTeamSession
	}
	card, target, err := h.Resolve(ctx, caller.OwnerID, in.To)
	if err != nil {
		return nil, err
	}
	if target.ID == caller.ID {
		return nil, ErrSelf
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, errors.New("message is empty")
	}
	contextID, depth, err := h.admit(caller.ID, in.ContextID, in.Mention && !in.Human)
	if err != nil {
		return nil, err
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(in.Text))
	msg.ContextID = contextID
	msg.Metadata = map[string]any{metaFrom: caller.ID, metaDepth: depth, metaSession: in.CallerSession}

	cl, err := h.client(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("team: connect @%s: %w", target.Handle, err)
	}
	res, err := cl.SendMessage(ctx, &a2a.SendMessageRequest{
		Message: msg,
		Config:  &a2a.SendMessageConfig{ReturnImmediately: true},
	})
	if err != nil {
		return nil, fmt.Errorf("team: send to @%s: %w", target.Handle, err)
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		return nil, fmt.Errorf("team: @%s answered without a task", target.Handle)
	}

	h.mu.Lock()
	ref := h.tasks[task.ID]
	if ref == nil {
		ref = &taskRef{}
		h.tasks[task.ID] = ref
	}
	ref.agentID, ref.callerAgentID, ref.callerSession, ref.to = target.ID, caller.ID, in.CallerSession, target
	h.mu.Unlock()

	return h.wait(ctx, cl, task.ID, contextID, target, in.Wait)
}

// admit charges one turn to the context and works out the depth. A
// caller answering an inbound task continues that task's context.
func (h *Hub) admit(callerID, contextID string, afterReply bool) (string, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	depth := 1
	cur, ok := h.inflight[callerID]
	if !ok && afterReply {
		cur, ok = h.last[callerID]
	}
	if ok {
		depth = cur.depth + 1
		if contextID == "" {
			contextID = cur.contextID
		}
	}
	if depth > MaxDepth {
		return "", 0, ErrHopLimit
	}
	if contextID == "" {
		contextID = uuid.NewString()
	}
	if h.contexts[contextID] >= MaxContextTurns {
		return "", 0, ErrHopLimit
	}
	h.contexts[contextID]++
	return contextID, depth, nil
}

// wait polls the task until it settles or the wait runs out.
func (h *Hub) wait(ctx context.Context, cl *a2aclient.Client, id a2a.TaskID, contextID string, to Peer, wait time.Duration) (*Result, error) {
	switch {
	case wait == 0:
		wait = DefaultWait
	case wait > MaxWait:
		wait = MaxWait
	}
	deadline := time.Now().Add(wait)
	for {
		h.mu.Lock()
		ref := h.tasks[id]
		if ref.finished {
			ref.delivered = true
			out := &Result{TaskID: string(id), ContextID: contextID, State: stateName(ref.state), To: "@" + to.Handle, ReplyText: ref.reply}
			h.mu.Unlock()
			return out, nil
		}
		if wait < 0 || !time.Now().Before(deadline) {
			ref.waiterGone = true
			h.mu.Unlock()
			return &Result{
				TaskID: string(id), ContextID: contextID, State: stateName(a2a.TaskStateWorking), To: "@" + to.Handle,
				Note: "Still working. The reply is delivered into this conversation when it lands — end your turn; do not poll or resend.",
			}, nil
		}
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(h.poll):
		}
		// Reading through the client keeps the task store authoritative;
		// the ref only carries what the store does not (the reply text).
		if _, err := cl.GetTask(ctx, &a2a.GetTaskRequest{ID: id}); err != nil {
			return nil, fmt.Errorf("team: read task %s: %w", id, err)
		}
	}
}

// GetTask reports a task the caller sent.
func (h *Hub) GetTask(ctx context.Context, callerAgentID, taskID string) (*Result, error) {
	h.mu.Lock()
	ref, ok := h.tasks[a2a.TaskID(taskID)]
	if !ok || ref.callerAgentID != callerAgentID || callerAgentID == "" {
		h.mu.Unlock()
		return nil, ErrUnknownTask
	}
	agentID, to := ref.agentID, ref.to
	if ref.finished {
		// The executor reports before the store has applied its last
		// event; the ref is the fresher of the two.
		out := &Result{TaskID: taskID, State: stateName(ref.state), To: "@" + to.Handle, ReplyText: ref.reply}
		h.mu.Unlock()
		if t, err := h.handler(agentID).GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(taskID)}); err == nil {
			out.ContextID = t.ContextID
		}
		return out, nil
	}
	h.mu.Unlock()
	t, err := h.handler(agentID).GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(taskID)})
	if err != nil {
		return nil, err
	}
	out := &Result{TaskID: taskID, ContextID: t.ContextID, State: stateName(t.Status.State), To: "@" + to.Handle}
	if t.Status.Message != nil {
		out.ReplyText = messageText(t.Status.Message)
	}
	return out, nil
}

// finished is the executor telling the Hub a task settled. A caller that
// stopped waiting gets the reply delivered into its session.
func (h *Hub) finished(ctx context.Context, id a2a.TaskID, state a2a.TaskState, reply string) {
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil {
		// The executor can finish before Send registered the task.
		ref = &taskRef{}
		h.tasks[id] = ref
	}
	ref.finished, ref.state, ref.reply = true, state, reply
	deliver := ref.waiterGone && !ref.delivered && ref.callerSession != ""
	if deliver {
		ref.delivered = true
	}
	session, to := ref.callerSession, ref.to
	h.mu.Unlock()
	if !deliver || h.Notify == nil {
		return
	}
	if state == a2a.TaskStateCompleted && reply == "" {
		return // [silent]: nothing to hand back
	}
	text := fmt.Sprintf("Reply from %s [task %s, %s]:\n\n%s", to.Label(), id, stateName(state), reply)
	if err := h.Notify.Deliver(context.WithoutCancel(ctx), session, text); err != nil {
		_ = err // best-effort: the task store still holds the result
	}
}

// stateName shortens an A2A state to the word the tool reports.
func stateName(s a2a.TaskState) string {
	return strings.ToLower(strings.TrimPrefix(string(s), "TASK_STATE_"))
}

// messageText joins a message's text parts.
func messageText(m *a2a.Message) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, p := range m.Parts {
		if t := p.Text(); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n")
}
