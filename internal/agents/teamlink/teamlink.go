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
	"sort"
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
	// MaxWait clamps a caller-supplied wait. It stays well under the
	// connector op timeout (3m): a wait past that aborts the whole call
	// instead of handing back state=working.
	MaxWait = 150 * time.Second

	// TaskTTL is how long a settled task and an idle context are kept.
	TaskTTL = time.Hour
	// LastTTL is how long a reply's exchange stays open to an @mention in
	// that reply.
	LastTTL = 10 * time.Minute
	// MaxTasks caps the tasks remembered at once; the oldest go first.
	MaxTasks = 1000
	// pruneEvery spaces the janitor passes Send triggers.
	pruneEvery = time.Minute
)

// Metadata keys carried on every message. They are set by the Hub from
// the calling SESSION, never from model input.
const (
	metaFrom    = "wick.from_agent"
	metaDepth   = "wick.depth"
	metaSession = "wick.caller_session"
	// metaChatUser names the recipient a shared agent's turn is for.
	metaChatUser = "wick.chat_user"
	// metaNewChat asks for the turn in a fresh chat of the target
	// instead of its main one (SendInput.NewChat).
	metaNewChat = "wick.new_chat"
)

var (
	// ErrHopLimit ends a chain that has gone on long enough. Worded for
	// the model reading it.
	ErrHopLimit = errors.New("hop limit reached — summarise and report to the user")
	// ErrUnknownHandle means no enabled agent of the owner has the handle.
	ErrUnknownHandle = errors.New("unknown or disabled Team handle")
	// ErrSelf refuses a message to the calling agent itself.
	// ErrSharedHumanOnly refuses an agent's turn for an agent another
	// owner shared: only the person it was shared with may mention it.
	// ErrNotShared ends a turn for a shared agent its owner unshared or
	// turned off since the mention was sent.
	ErrNotShared       = errors.New("that agent is no longer shared with you or is turned off")
	ErrSharedHumanOnly = errors.New("that agent is shared with your user, not your Team — only you can @mention it")
	ErrSelf            = errors.New("you can't message yourself — pick a teammate from your Team")
	// ErrNotTeamSession refuses a caller that is not a Team agent.
	ErrNotTeamSession = errors.New("team messaging is only available in a Team agent's session")
	// ErrUnknownContext refuses a context_id this owner never opened (or
	// one already aged out).
	ErrUnknownContext = errors.New("unknown context_id — omit it to start a new exchange")
	// ErrNewChatUnsupported refuses new_chat where Turns cannot open one.
	ErrNewChatUnsupported = errors.New("new_chat is not available here — send without it to reach the main chat")
	// ErrUnknownTask means the task id is not known to this caller: never
	// issued to it, or lost when wick restarted (tasks live in memory).
	ErrUnknownTask = errors.New("unknown task id — never issued to you, or lost when wick restarted (tasks are kept in memory only)")
	// ErrTaskExpired is an ErrUnknownTask for a task this caller did send
	// but whose record was cleaned up (TaskTTL after it settled).
	ErrTaskExpired = fmt.Errorf("%w: this task expired — its result is kept 1 hour after it settles; its reply was already delivered into the conversation that sent it", ErrUnknownTask)
	// ErrMentionsOff refuses a turn the target's mention setting does not
	// take from the caller (Peer.AcceptsFrom).
	ErrMentionsOff = errors.New("that agent does not take turns from you — tell the user instead")
	// ErrRemoteOwnerOnly refuses an agent's turn for a remote agent whose
	// owner keeps it to themselves (Mention "Nobody", or the old usage
	// "only_me"). Send returns it wrapped in an OwnerOnlyError that names
	// the agent and the way out.
	ErrRemoteOwnerOnly = errors.New("that remote agent is set to take messages from its owner only — tell the user instead")
)

// OwnerOnlyError is ErrRemoteOwnerOnly for one agent: it says which
// setting blocks the turn and how the owner lifts it, so the caller can
// explain instead of guessing.
type OwnerOnlyError struct{ Handle string }

func (e *OwnerOnlyError) Error() string {
	return fmt.Sprintf("@%s is a remote agent set to \"Nobody\" in its Settings › Mention, so only its owner may use it — "+
		"tell the user; the owner can switch it to \"Any of my agents\" there", e.Handle)
}

// Is makes errors.Is(err, ErrRemoteOwnerOnly) hold.
func (e *OwnerOnlyError) Is(target error) bool { return target == ErrRemoteOwnerOnly }

// Peer is one Team agent as the registry sees it.
type Peer struct {
	ID, OwnerID, Handle        string
	Name, Tagline, Description string
	IsCaptain, Disabled        bool
	// MentionFrom, MentionAllow and MaxHops are the agent's mention
	// settings (see policy.go).
	MentionFrom  string
	MentionAllow []string
	MaxHops      int
	// Remote marks a remote agent (A2A, Slack, plugin): its turn goes out
	// of wick, so it gets only the mention text, without the framing that
	// names the sender. RemoteOwnerOnly refuses every agent's turn — the
	// old usage "only_me" not yet carried into MentionFrom; a person's
	// mention still goes through.
	Remote, RemoteOwnerOnly bool
	// ChatUser is whose conversation with the agent a turn runs in: ""
	// for the owner's own main chat, a recipient's id for an agent shared
	// with them (see SharedDirectory).
	ChatUser string
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

// SharedDirectory is optionally implemented by a Directory that knows
// agents shared with a user (chat only). A shared agent answers a
// person's @mention from the recipient's own sessions, never an agent's,
// and its turn runs in the recipient's chat with it.
type SharedDirectory interface {
	// SharedPeers lists the enabled agents other owners share with
	// userID, each with ChatUser = userID.
	SharedPeers(ctx context.Context, userID string) ([]Peer, error)
}

// sharedPeer finds the agent called handle among those shared with userID.
func (h *Hub) sharedPeer(ctx context.Context, userID, handle string) (Peer, bool) {
	sd, ok := h.Dir.(SharedDirectory)
	if !ok || userID == "" {
		return Peer{}, false
	}
	all, err := sd.SharedPeers(ctx, userID)
	if err != nil {
		return Peer{}, false
	}
	for _, p := range all {
		if p.Handle == handle && !p.Disabled {
			p.ChatUser = userID
			return p, true
		}
	}
	return Peer{}, false
}

// sharedByID is sharedPeer by agent id: whether agentID is still shared
// with userID.
func (h *Hub) sharedByID(ctx context.Context, userID, agentID string) (Peer, bool) {
	sd, ok := h.Dir.(SharedDirectory)
	if !ok || userID == "" {
		return Peer{}, false
	}
	all, err := sd.SharedPeers(ctx, userID)
	if err != nil {
		return Peer{}, false
	}
	for _, p := range all {
		if p.ID == agentID && !p.Disabled {
			p.ChatUser = userID
			return p, true
		}
	}
	return Peer{}, false
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

// Refusal is a message the Hub would not send: the hop budget ran out, or
// an @mention named someone who does not take mentions.
type Refusal struct {
	// Session is the caller's conversation — where the event is shown.
	Session   string
	From, To  string
	ContextID string
	// HopLimit is true for an exhausted budget; false for a refused mention.
	HopLimit bool
	// MaxTurns is the exhausted budget's size, for the event text.
	MaxTurns int
	Err      error
}

// RefusalNotifier is optionally implemented by a Notifier that records
// refusals in the caller's thread.
type RefusalNotifier interface {
	Refused(ctx context.Context, r Refusal)
}

// refused reports r to the Notifier when it records refusals.
func (h *Hub) refused(ctx context.Context, r Refusal) {
	if rn, ok := h.Notify.(RefusalNotifier); ok && r.Session != "" {
		rn.Refused(context.WithoutCancel(ctx), r)
	}
}

// Handoff is one mention_handoff audit event.
type Handoff struct {
	From, To, ContextID, TaskID string
	// ToID is the target agent's id, so the thread can link to its chat.
	ToID  string
	State a2a.TaskState
	// FromSession is the caller's conversation, so a reply the target
	// sends after the task ended can still find its way back after a
	// restart (the Hub's own memory of the task does not survive one).
	FromSession string
}

// Hub is the registry, the client and the bookkeeping, in one place so
// every entry point (team_message, @mention routing) shares one budget.
type Hub struct {
	Dir    Directory
	Turns  Turns
	Notify Notifier

	// poll is how often a waiting call re-reads its task. Tests shorten it.
	poll time.Duration
	// maxWait clamps a caller's wait (MaxWait). Tests shorten it.
	maxWait time.Duration
	// now is the clock; tests move it.
	now func() time.Time

	mu       sync.Mutex
	handlers map[string]a2asrv.RequestHandler
	stores   map[string]*genStore
	tasks    map[a2a.TaskID]*taskRef
	// gone notes the caller of each task the janitor dropped, for goneTTL,
	// so GetTask can say "expired" rather than "never issued".
	gone     map[a2a.TaskID]goneTask
	contexts map[string]*contextState
	// inflight holds every inbound task an agent is answering right now,
	// per task: two messages to one agent run side by side and neither
	// may overwrite or clear the other's chain.
	inflight map[string]map[a2a.TaskID]inbound
	// pruned is when the janitor last ran.
	pruned time.Time
	// last is the inbound task each agent answered most recently. A
	// mention in that answer continues it, so "@a thanks" → "@b thanks"
	// stays inside one context's turn budget instead of starting fresh.
	last map[string]inbound
	// answered is the task each session answered most recently, so a
	// reply it sends after that task ended (FollowUp) reaches the asker.
	answered map[string]a2a.TaskID
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
	// touched is the last time the task changed; the janitor ages from it.
	touched time.Time
	// contextID, title and started describe the task for the UI list.
	contextID string
	title     string
	started   time.Time
}

// contextState is one exchange's turn count.
type contextState struct {
	turns   int
	touched time.Time
	// owner is the user whose agents opened the exchange; nobody else's
	// agent may continue it.
	owner string
	// limit is the exchange's turn budget: the smallest MaxHops of every
	// agent that took part so far.
	limit int
	// chats is the session each agent answers this exchange in when it
	// was opened with new_chat (chatKey → session id); an agent missing
	// here answers in its main chat.
	chats map[string]string
}

// inbound is the task an agent is currently answering. A message the
// agent sends during that turn continues its context, one level deeper.
type inbound struct {
	contextID string
	depth     int
	at        time.Time
	// session is the conversation answering it, when Turns can say.
	session string
}

// SessionLocator is optionally implemented by Turns: the session a turn
// of agent will run in. It lets a message sent from that session be tied
// to the inbound task it is answering.
type SessionLocator interface {
	MainSession(ctx context.Context, agent Peer) string
}

// ChatOpener is optionally implemented by Turns: a fresh chat of an agent
// beside its main one, and a turn in a chosen chat. new_chat needs it.
type ChatOpener interface {
	// NewChat opens a new, non-main chat of agent and returns its id.
	NewChat(ctx context.Context, agent Peer) (string, error)
	// RunIn is Run in sessionID instead of the main chat.
	RunIn(ctx context.Context, agent Peer, sessionID, text string) (string, string, error)
}

// chatKey names one agent's side of an exchange: a shared agent's chat
// with a recipient is not its owner's.
func chatKey(p Peer) string { return p.ID + "\x00" + p.ChatUser }

// NewHub returns an empty Hub.
func NewHub(dir Directory, turns Turns, notify Notifier) *Hub {
	return &Hub{
		Dir: dir, Turns: turns, Notify: notify,
		poll:     250 * time.Millisecond,
		maxWait:  MaxWait,
		now:      time.Now,
		handlers: map[string]a2asrv.RequestHandler{},
		stores:   map[string]*genStore{},
		tasks:    map[a2a.TaskID]*taskRef{},
		gone:     map[a2a.TaskID]goneTask{},
		contexts: map[string]*contextState{},
		inflight: map[string]map[a2a.TaskID]inbound{},
		last:     map[string]inbound{},
		answered: map[string]a2a.TaskID{},
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
	// The owner's own agents win a handle clash with a shared one.
	if p, ok := h.sharedPeer(ctx, ownerID, handle); ok {
		return Card(p), p, nil
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
	// NewChat runs the turn in a new chat of the target instead of its
	// main one; later messages of the same context_id go to that chat.
	NewChat bool
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
	if err == nil && target.ID == caller.ID {
		err = ErrSelf
	}
	// A person's @mention always goes through; an agent's needs the
	// target's consent.
	// An agent shared with the caller's owner takes a person's mention
	// only: agents never hand turns across owners.
	if err == nil && target.OwnerID != caller.OwnerID && !in.Human {
		err = ErrSharedHumanOnly
	}
	if err == nil && !in.Human && !target.AcceptsFrom(caller) {
		err = ErrMentionsOff
		if target.Remote && (target.RemoteOwnerOnly || NormalizeMentionFrom(target.MentionFrom) == MentionOff) {
			err = &OwnerOnlyError{Handle: target.Handle}
		}
	}
	if err != nil {
		if in.Mention {
			h.refused(ctx, Refusal{Session: in.CallerSession, From: caller.Handle, To: NormalizeHandle(in.To), Err: err})
		}
		return nil, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, errors.New("message is empty")
	}
	if _, ok := h.Turns.(ChatOpener); in.NewChat && !ok {
		return nil, ErrNewChatUnsupported
	}
	contextID, depth, limit, err := h.admit(caller.OwnerID, caller.ID, in.CallerSession, in.ContextID, in.Mention && !in.Human, MinHops(caller, target))
	if err != nil {
		if errors.Is(err, ErrHopLimit) {
			h.refused(ctx, Refusal{Session: in.CallerSession, From: caller.Handle, To: target.Handle, ContextID: in.ContextID, HopLimit: true, MaxTurns: limit, Err: err})
		}
		return nil, err
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(in.Text))
	msg.ContextID = contextID
	msg.Metadata = map[string]any{metaFrom: caller.ID, metaDepth: depth, metaSession: in.CallerSession}
	if target.ChatUser != "" {
		msg.Metadata[metaChatUser] = target.ChatUser
	}
	if in.NewChat {
		msg.Metadata[metaNewChat] = true
	}

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
	ref.touched = h.now()
	ref.contextID, ref.title, ref.started = contextID, firstLine(in.Text), ref.touched
	h.mu.Unlock()

	return h.wait(ctx, cl, task.ID, contextID, target, in.Wait)
}

// admit charges one turn to the context and works out the depth. A
// caller answering an inbound task continues that task's context. limit is
// the cap of the two agents of this message; the context keeps the
// smallest cap it has seen, which admit returns.
func (h *Hub) admit(ownerID, callerID, callerSession, contextID string, afterReply bool, limit int) (string, int, int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked()
	depth := 1
	cur, ok := h.inboundOf(callerID, callerSession)
	if !ok && afterReply {
		cur, ok = h.last[callerID]
	}
	switch {
	case ok:
		// Inside a chain the chain's context is the only one: a different
		// or made-up context_id would otherwise reset the turn budget.
		depth = cur.depth + 1
		contextID = cur.contextID
	case contextID != "":
		// Only an exchange this owner already has may be continued.
		if cs := h.contexts[contextID]; cs == nil || cs.owner != ownerID {
			return "", 0, limit, ErrUnknownContext
		}
	}
	if depth > MaxDepth {
		return "", 0, limit, ErrHopLimit
	}
	if contextID == "" {
		contextID = uuid.NewString()
	}
	cs := h.contexts[contextID]
	if cs == nil {
		cs = &contextState{owner: ownerID, limit: limit}
		h.contexts[contextID] = cs
	}
	if limit < cs.limit || cs.limit <= 0 {
		cs.limit = limit
	}
	if cs.turns >= cs.limit {
		return "", 0, cs.limit, ErrHopLimit
	}
	cs.turns++
	cs.touched = h.now()
	return contextID, depth, cs.limit, nil
}

// inboundOf picks the inbound task a message from callerID continues:
// the one running in callerSession when that is known, else the deepest
// — with several in flight and no way to tell them apart, the strictest
// chain is the one whose budget must not be dodged.
func (h *Hub) inboundOf(callerID, callerSession string) (inbound, bool) {
	deepest := func(match func(inbound) bool) (inbound, bool) {
		var best inbound
		found := false
		for _, in := range h.inflight[callerID] {
			if match(in) && (!found || in.depth > best.depth) {
				best, found = in, true
			}
		}
		return best, found
	}
	if callerSession != "" {
		if in, ok := deepest(func(in inbound) bool { return in.session == callerSession }); ok {
			return in, true
		}
	}
	return deepest(func(inbound) bool { return true })
}

// goneTTL is how long a dropped task is still known as expired.
const goneTTL = 24 * time.Hour

// goneTask is a dropped task's caller and when it was dropped.
type goneTask struct {
	caller string
	at     time.Time
}

// pruneLocked is the janitor: settled tasks and idle contexts older than
// TaskTTL, reply exchanges older than LastTTL, tasks past MaxTasks
// (oldest first), and the task stores' old generation. Runs from Send at
// most every pruneEvery, unless the task cap is already exceeded.
func (h *Hub) pruneLocked() {
	now := h.now()
	if now.Sub(h.pruned) < pruneEvery && len(h.tasks) <= MaxTasks {
		return
	}
	h.pruned = now
	for id, t := range h.tasks {
		// A task nobody finished is still dropped after 2×TTL: its turn
		// is long gone, and the ref would otherwise never leave.
		if age := now.Sub(t.touched); (t.finished && age > TaskTTL) || age > 2*TaskTTL {
			h.dropLocked(id, now)
		}
	}
	for id, g := range h.gone {
		if now.Sub(g.at) > goneTTL || len(h.gone) > 8*MaxTasks {
			delete(h.gone, id)
		}
	}
	if extra := len(h.tasks) - MaxTasks; extra > 0 {
		ids := make([]a2a.TaskID, 0, len(h.tasks))
		for id := range h.tasks {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return h.tasks[ids[i]].touched.Before(h.tasks[ids[j]].touched) })
		for _, id := range ids[:extra] {
			h.dropLocked(id, now)
		}
	}
	for id, c := range h.contexts {
		if now.Sub(c.touched) > TaskTTL {
			delete(h.contexts, id)
		}
	}
	for id, l := range h.last {
		if now.Sub(l.at) > LastTTL {
			delete(h.last, id)
		}
	}
	for _, st := range h.stores {
		st.rotate(now, TaskTTL)
	}
}

// dropLocked forgets task id, noting it as gone.
func (h *Hub) dropLocked(id a2a.TaskID, now time.Time) {
	if t := h.tasks[id]; t != nil && h.gone != nil {
		h.gone[id] = goneTask{caller: t.callerAgentID, at: now}
	}
	delete(h.tasks, id)
}

// unknownLocked is the error of a task id caller has no record of.
func (h *Hub) unknownLocked(callerAgentID string, id a2a.TaskID) error {
	if g, ok := h.gone[id]; ok && g.caller == callerAgentID && callerAgentID != "" {
		return ErrTaskExpired
	}
	return ErrUnknownTask
}

// chatFor is the session target answers contextID in: a new chat when
// newChat asks for one (remembered for the rest of the exchange), the
// chat an earlier new_chat opened, else "" — its main chat.
func (h *Hub) chatFor(ctx context.Context, contextID string, target Peer, newChat bool) (string, error) {
	key := chatKey(target)
	if !newChat {
		h.mu.Lock()
		defer h.mu.Unlock()
		if cs := h.contexts[contextID]; cs != nil {
			return cs.chats[key], nil
		}
		return "", nil
	}
	op, ok := h.Turns.(ChatOpener)
	if !ok {
		return "", ErrNewChatUnsupported
	}
	id, err := op.NewChat(ctx, target)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if cs := h.contexts[contextID]; cs != nil {
		if cs.chats == nil {
			cs.chats = map[string]string{}
		}
		cs.chats[key] = id
	}
	return id, nil
}

// wait polls the task until it settles or the wait runs out.
func (h *Hub) wait(ctx context.Context, cl *a2aclient.Client, id a2a.TaskID, contextID string, to Peer, wait time.Duration) (*Result, error) {
	switch {
	case wait == 0:
		wait = DefaultWait
	case wait > h.maxWait:
		wait = h.maxWait
	}
	deadline := time.Now().Add(wait)
	for {
		h.mu.Lock()
		ref := h.tasks[id]
		if ref == nil {
			// Pruned under a caller that waited past TaskTTL.
			h.mu.Unlock()
			return nil, ErrTaskExpired
		}
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
		err := h.unknownLocked(callerAgentID, a2a.TaskID(taskID))
		h.mu.Unlock()
		return nil, err
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
	ref.finished, ref.state, ref.reply, ref.touched = true, state, reply, h.now()
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

// FollowUp hands text — a reply the agent answering in sessionID sent
// after its task had ended, such as a remote agent's late message — to
// whoever asked, delivered into the asker's session like a late reply.
// False when the session answered no task the Hub still remembers.
func (h *Hub) FollowUp(ctx context.Context, sessionID, text string) bool {
	if sessionID == "" || strings.TrimSpace(text) == "" || h.Notify == nil {
		return false
	}
	h.mu.Lock()
	id, ok := h.answered[sessionID]
	ref := h.tasks[id]
	var session string
	var to Peer
	if ok && ref != nil {
		session, to = ref.callerSession, ref.to
	}
	h.mu.Unlock()
	if session == "" {
		return false
	}
	msg := fmt.Sprintf("Follow-up from %s [task %s]:\n\n%s", to.Label(), id, text)
	return h.Notify.Deliver(context.WithoutCancel(ctx), session, msg) == nil
}

// TaskView is one task a session sent, as the Sub-agents panel lists it.
type TaskView struct {
	TaskID    string    `json:"task_id"`
	ContextID string    `json:"context_id"`
	ToID      string    `json:"to_agent_id"`
	ToHandle  string    `json:"to_handle"`
	ToName    string    `json:"to_name"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	Turns     int       `json:"turns"`
	MaxTurns  int       `json:"max_turns"`
	Started   time.Time `json:"started_at"`
	Updated   time.Time `json:"updated_at"`
}

// SentFrom lists the tasks sessionID sent and the Hub still remembers,
// newest first. Unfinished tasks read "working".
func (h *Hub) SentFrom(sessionID string) []TaskView {
	if sessionID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pruneLocked()
	out := []TaskView{}
	for id, ref := range h.tasks {
		if ref.callerSession != sessionID || ref.started.IsZero() {
			continue
		}
		state := "working"
		if ref.finished {
			state = stateName(ref.state)
		}
		turns, limit := 0, MaxContextTurns
		if c := h.contexts[ref.contextID]; c != nil {
			turns, limit = c.turns, c.limit
		}
		out = append(out, TaskView{
			TaskID: string(id), ContextID: ref.contextID,
			ToID: ref.to.ID, ToHandle: ref.to.Handle, ToName: ref.to.Name,
			Title: ref.title, State: state, Turns: turns, MaxTurns: limit,
			Started: ref.started, Updated: ref.touched,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// firstLine is the task's title: its first non-blank line, capped.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > 120 {
				return string(r[:119]) + "…"
			}
			return l
		}
	}
	return ""
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
