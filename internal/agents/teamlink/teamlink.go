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
	// QuickWait is how long a message waits for the reply before it hands
	// back state=working (A2A: SendMessage returns at once, the client
	// decides how long to look). It catches the quick answer of a local
	// teammate, and it is short because a provider sends the tool calls of
	// one turn one after another: five messages waiting 15s each still
	// return in about a minute, where the old 90-150s wait held the turn
	// for many minutes. Any reply that lands later is delivered into the
	// caller's conversation.
	QuickWait = 15 * time.Second
	// fanOutWindow: a message sent while another one from the same
	// conversation is still working, and was sent within this window,
	// does not wait at all — the asker is fanning out, not waiting on one.
	fanOutWindow = 5 * time.Minute
	// cancelWait bounds how long cancel_task waits for the task store to
	// take the cancellation.
	cancelWait = 5 * time.Second
	// answerRetry bounds how long an answer to an input_required task
	// waits for the task's last turn to close.
	answerRetry = 5 * time.Second

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
	// metaOrigin is OriginUser on a task a person's @mention started
	// (SendInput.Human), so the handoff rows can say so.
	metaOrigin = "wick.origin"
	// metaChatUser names the recipient a shared agent's turn is for.
	metaChatUser = "wick.chat_user"
	// metaNewChat asks for the turn in a fresh chat of the target
	// instead of its main one (SendInput.NewChat).
	metaNewChat = "wick.new_chat"
	// metaChat resumes one of the target's chats the person asked to
	// continue (SendInput.Chat).
	metaChat = "wick.chat"
	// metaSessionUser names the person who wrote an @mention in their
	// chat with an agent another owner shared with them
	// (SendInput.SessionUser); the executor re-checks both ends against
	// that person.
	metaSessionUser = "wick.session_user"
)

var (
	// ErrHopLimit ends a chain that has gone on long enough. Worded for
	// the model reading it.
	ErrHopLimit = errors.New("hop limit reached — summarise and report to the user")
	// ErrUnknownHandle means no enabled agent of the owner has the handle.
	ErrUnknownHandle = errors.New("unknown or disabled Team handle")
	// ErrSelf refuses a message to the calling agent itself.
	// ErrSharedHumanOnly refuses an agent's turn for an agent of another
	// owner that is not shared with the caller's owner.
	// ErrNotShared ends a turn for a shared agent its owner unshared or
	// turned off since the mention was sent.
	ErrNotShared       = errors.New("that agent is no longer shared with you or is turned off")
	ErrSharedHumanOnly = errors.New("that agent belongs to another owner and is not shared with yours — tell the user instead")
	ErrSelf            = errors.New("you can't message yourself — pick a teammate from your Team")
	// ErrNotTeamSession refuses a caller that is not a Team agent.
	ErrNotTeamSession = errors.New("team messaging is only available in a Team agent's session")
	// ErrUnknownContext refuses a context_id this owner never opened (or
	// one already aged out).
	ErrUnknownContext = errors.New("unknown context_id — omit it to start a new exchange")
	// ErrNewChatUnsupported refuses new_chat where Turns cannot open one.
	ErrNewChatUnsupported = errors.New("new_chat is not available here — send without it to reach the main chat")
	// ErrUnknownTask means the task id is not known to this caller: never
	// issued to it, or older than TaskKeep.
	ErrUnknownTask = errors.New("unknown task id — never issued to you, or older than 7 days")
	// ErrTaskExpired is an ErrUnknownTask for a task this caller did send
	// but whose record was cleaned up.
	ErrTaskExpired = fmt.Errorf("%w: this task expired — its reply was already delivered into the conversation that sent it", ErrUnknownTask)
	// ErrTaskContext refuses a task_id sent with a context_id it does not
	// belong to (A2A §3.4.3).
	ErrTaskContext = errors.New("task_id does not belong to that context_id — pass the context_id the task returned, or omit it")
	// ErrTaskNotWaiting refuses an answer to a task that asked nothing.
	ErrTaskNotWaiting = errors.New("that task is not waiting for input — send without task_id to start a new one")
	// ErrTaskOtherAgent refuses a task_id sent to another teammate.
	ErrTaskOtherAgent = errors.New("task_id belongs to another teammate — omit to, or use the teammate the task went to")
	// ErrTaskSettled refuses cancelling a task that already ended.
	ErrTaskSettled = errors.New("that task already ended — nothing to cancel")
	// ErrAlreadyAnswered refuses a second answer to a teammate's question:
	// the first one, from the user or the sender, won.
	ErrAlreadyAnswered = errors.New("that question was already answered — the first answer won")
	// ErrUserTask refuses an agent's answer to (or cancel of) a task the
	// person started with an @mention in its chat: only they act on it.
	ErrUserTask = errors.New("this task belongs to the user: they sent it with an @mention in this chat, so only the user can answer or cancel it from the chat — do not answer it yourself")
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
// agents shared with a user (chat only). A shared agent answers the
// recipient's @mention and the recipient's own agents, never a third
// owner's, and its turn runs in the recipient's chat with it.
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

// TurnEnd is what a Turns implementation returns, as the error, for a turn
// that ended in an A2A state other than completed or failed — a remote
// agent that asked a question (input_required) or refused (rejected).
// Text is the question or the reason.
type TurnEnd struct {
	State a2a.TaskState
	Text  string
}

func (e *TurnEnd) Error() string { return stateName(e.State) + ": " + e.Text }

// InputRequiredToken opens a teammate's reply that asks the sender a
// question before it can go on: the task ends as input_required and the
// sender answers with the same task_id.
const InputRequiredToken = "[input-required]"

// Handoff is one mention_handoff audit event.
type Handoff struct {
	From, To, ContextID, TaskID string
	// ToID is the target agent's id, so the thread can link to its chat.
	ToID  string
	State a2a.TaskState
	// Origin is OriginUser when a person's @mention started the task.
	Origin string
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
	// maxWait is the default and the cap of a caller's wait (QuickWait).
	// Tests shorten it.
	maxWait time.Duration
	// dir keeps the tasks on disk (Persist); "" = memory only.
	dir string
	// saveMu orders the writes of task files (persist).
	saveMu sync.Mutex
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
	// linkMu serialises pairing a caller's conversation with a target's
	// chat (chatFor), so two messages sent at once open one chat, not two.
	linkMu sync.Mutex
	// CallerBusy reports whether a session runs (or waits for) a turn. A
	// question nobody's turn is handling waits for the user (NeedsYou);
	// nil = never busy.
	CallerBusy func(sessionID string) bool
	// OnTaskChange is told every time a task changes (sent, settled,
	// answered, canceled), with the sending session and the task as
	// SentFrom lists it, so open viewers update without polling. Called
	// in order per task, never with h.mu held; nil = nobody listens.
	OnTaskChange func(sessionID string, v TaskView)
	// PredecessorBusy reports whether a previous wick process, still
	// draining, may be running a turn of chat ("" = the teammate's chat
	// is not known: any turn). A task it may still finish is not settled
	// as interrupted yet. nil = there is never a predecessor.
	PredecessorBusy func(chat string) bool
	// orphanPoll is how often the tasks a restart left working are
	// checked (settleOrphans). Tests shorten it.
	orphanPoll time.Duration
}

// taskRef is what the Hub remembers about a task it sent.
type taskRef struct {
	agentID       string
	callerAgentID string
	// callerOwner owns the exchange, kept so a task read back from disk
	// can still be continued.
	callerOwner   string
	callerSession string
	to            Peer
	// canceled: cancel_task ended the task; whatever the turn still
	// reports afterwards is ignored.
	canceled bool
	// answered is the target's session that answered (FollowUp).
	answered string
	// sent holds the dedupe keys (textKey) of what the caller already got.
	sent map[string]bool
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
	// chat is the target's session the turn runs in, once the executor
	// picked it ("" = main or not yet known).
	chat string
	// claimed: an answer to the task's question is on its way (answering
	// took it under h.mu); any other answer is refused until the turn it
	// starts ends, or the send fails and gives the question back.
	claimed bool
	// userAnswer: the claim is the user's answer (AnswerFromUser).
	userAnswer bool
	// fromDisk: the task was read back from disk and nothing in this
	// process has run it since. Unfinished, it was working in a process
	// that is gone (or still draining): see settleOrphans.
	fromDisk bool
	// interrupted: the task was settled failed because the process
	// running it went away (settleOrphans).
	interrupted bool
	// origin is OriginUser for a task a person's @mention started in the
	// caller's chat (SendInput.Human), set by Send and never from model
	// input; "" = the agent's own task. Only that person answers or
	// cancels it, and nothing of it is delivered into the agent's chat.
	origin string
	// originUser is the SessionUser that mention resolved as (the user of
	// the caller's chat, "" = unknown): the person's answer resolves the
	// same way again, so it reaches the same teammate through the same
	// share.
	originUser string
}

// OriginUser is the origin of a task a person started (taskRef.origin).
const OriginUser = "user"

// userTask reports whether the person, not the agent, owns ref.
func (r *taskRef) userTask() bool { return r.origin == OriginUser }

// answerClaim is what answering changed on a task to claim it, so a send
// that fails gives back exactly that.
type answerClaim struct {
	waiterGone, delivered bool
	reply                 string
	state                 a2a.TaskState
	// origin and originUser are the claimed task's (taskRef.origin): an
	// answer to the person's own task carries them on.
	origin, originUser string
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

// ChatLinker is optionally implemented by a ChatOpener: it pairs a
// caller's conversation with one chat of each agent it messages, so a new
// conversation at the caller gets a new chat at the target (a new Slack
// thread for a Slack remote) instead of piling into its main chat. The
// pair lives on the target chat's session, so it outlives the Hub.
type ChatLinker interface {
	// LinkedChat is the chat of agent (for agent.ChatUser) paired with
	// callerSession, "" for none. A callerSession that is itself paired
	// with a chat of agent answers with that chat, so a reply to the
	// asker lands back in the conversation that asked.
	LinkedChat(ctx context.Context, agent Peer, callerSession string) string
	// Link pairs chat with callerSession, unpairing any other chat of
	// agent paired with it.
	Link(ctx context.Context, agent Peer, chat, callerSession string) error
}

// chatKey names one agent's side of an exchange: a shared agent's chat
// with a recipient is not its owner's.
func chatKey(p Peer) string { return p.ID + "\x00" + p.ChatUser }

// NewHub returns an empty Hub.
func NewHub(dir Directory, turns Turns, notify Notifier) *Hub {
	return &Hub{
		Dir: dir, Turns: turns, Notify: notify,
		poll:       250 * time.Millisecond,
		orphanPoll: orphanPoll,
		maxWait:    QuickWait,
		now:        time.Now,
		handlers:   map[string]a2asrv.RequestHandler{},
		stores:     map[string]*genStore{},
		tasks:      map[a2a.TaskID]*taskRef{},
		gone:       map[a2a.TaskID]goneTask{},
		contexts:   map[string]*contextState{},
		inflight:   map[string]map[a2a.TaskID]inbound{},
		last:       map[string]inbound{},
		answered:   map[string]a2a.TaskID{},
	}
}

// SetQuickWait changes how long a message waits for its reply (QuickWait).
// Tests outside the package use it.
func (h *Hub) SetQuickWait(d time.Duration) { h.maxWait = d }

// NormalizeHandle trims a typed handle the way team.NormalizeHandle does.
func NormalizeHandle(h string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "@"))
}

// Reachable lists the enabled agents agentID may message, itself
// excluded: its owner's other agents, then the agents other owners share
// with its owner (ChatUser = the owner) whose handle no own agent takes.
// The tool is offered only when this is non-empty.
func (h *Hub) Reachable(ctx context.Context, agentID string) ([]Peer, error) {
	self, err := h.Dir.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return h.reachableFor(ctx, self.OwnerID, self.ID)
}

// MentionTargets lists the handles a person's @mention reaches from a
// session of agentID whose user is sessionUser. In the agent's owner's
// own sessions that is Reachable; in a chat with an agent shared with
// sessionUser it is sessionUser's own agents and those shared with them —
// never the sharing owner's other agents.
func (h *Hub) MentionTargets(ctx context.Context, agentID, sessionUser string) ([]Peer, error) {
	self, err := h.Dir.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	user := self.OwnerID
	if sessionUser != "" {
		user = sessionUser
	}
	return h.reachableFor(ctx, user, self.ID)
}

// reachableFor is ownerID's enabled agents then those shared with ownerID,
// one per handle (own first), selfID excluded.
func (h *Hub) reachableFor(ctx context.Context, ownerID, selfID string) ([]Peer, error) {
	all, err := h.Dir.Peers(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]Peer, 0, len(all))
	seen := map[string]bool{}
	for _, p := range all {
		if p.ID != selfID && !p.Disabled && p.OwnerID == ownerID {
			out = append(out, p)
			seen[p.Handle] = true
		}
	}
	if sd, ok := h.Dir.(SharedDirectory); ok && ownerID != "" {
		shared, err := sd.SharedPeers(ctx, ownerID)
		if err != nil {
			return out, nil
		}
		for _, p := range shared {
			if p.ID != selfID && !p.Disabled && !seen[p.Handle] {
				p.ChatUser = ownerID
				out = append(out, p)
				seen[p.Handle] = true
			}
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
	// TaskID answers a task that ended input_required: the message goes
	// to that task, in its context, at the teammate it went to.
	TaskID string
	// Wait bounds the synchronous part. 0 = QuickWait (none while the
	// conversation fans out, see fanOutWindow); negative = do not wait at
	// all (the @mention router); larger values are capped at QuickWait.
	Wait time.Duration
	// Mention marks an @handle line in an agent's own reply: it continues
	// the exchange that reply answered. A person's mention (Human) always
	// starts a fresh exchange.
	Mention, Human bool
	// NewChat runs the turn in a new chat of the target instead of the
	// one paired with CallerSession, and pairs that new chat instead.
	NewChat bool
	// Chat resumes this chat of the target (a session_id RecentChats
	// listed) and pairs it with CallerSession — only when the person
	// asked to continue it.
	Chat string
	// SessionUser is the user of CallerSession, resolved from the session
	// like CallerSession. For a person's mention (Human) in their chat
	// with an agent another owner shared with them, the mention resolves
	// against this person, not the agent's owner.
	SessionUser string
	// byUser marks the user's own answer (AnswerFromUser), which holds the
	// task's userAnswer claim.
	byUser bool
}

// Result is what a caller gets back.
type Result struct {
	TaskID    string `json:"task_id"`
	ContextID string `json:"context_id"`
	State     string `json:"state"`
	To        string `json:"to"`
	ReplyText string `json:"reply_text,omitempty"`
	// Reason says why a task ended failed, rejected or canceled.
	Reason string `json:"reason,omitempty"`
	Note   string `json:"note,omitempty"`
	// Chat is the target's chat the turn runs in — the one paired with
	// the caller's conversation — with its Slack thread for a Slack
	// remote; nil for the main chat or when not known yet.
	Chat *ChatInfo `json:"chat,omitempty"`
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
	var answer a2a.TaskID
	var claim *answerClaim
	if in.TaskID = strings.TrimSpace(in.TaskID); in.TaskID != "" {
		if in.To, in.ContextID, claim, err = h.answering(caller.ID, in); err != nil {
			return nil, err
		}
		answer = a2a.TaskID(in.TaskID)
		claimed := answer
		// Any way out before the teammate took the answer gives the
		// question back; claim is cleared once it did.
		defer func() {
			if claim != nil {
				h.unclaim(claimed, claim)
			}
		}()
		// Viewers see the task working again before its next turn reports.
		h.persist(answer)
		if in.byUser && claim.origin == OriginUser {
			// The person answering their own task continues the mention
			// that started it: the same resolution and the same freedom
			// from the target's mention policy. Only their answer (byUser,
			// from that chat's UI) does — an agent's was refused above.
			in.Human, in.SessionUser = true, claim.originUser
		}
	}
	// A person writing in their chat with an agent another owner shared
	// with them speaks for themselves: the mention reaches their own
	// agents and those shared with them, never the sharing owner's others.
	// An agent's message — a resume (Chat) included — never widens past a
	// plain send: it resolves against the agent's owner; a resume only
	// narrows which chat of that target it lands in (resumeScope).
	resolveAs := caller.OwnerID
	viaShare := in.Human && in.SessionUser != "" && in.SessionUser != caller.OwnerID
	if viaShare {
		resolveAs = in.SessionUser
	}
	card, target, err := h.Resolve(ctx, resolveAs, in.To)
	if err == nil && target.ID == caller.ID {
		err = ErrSelf
	}
	// A person's @mention always goes through; an agent's needs the
	// target's consent.
	// An agent reaches its owner's agents and those shared with its owner
	// (the turn then runs in the owner's chat with it); never a third
	// owner's.
	if err == nil && !in.Human && !viaShare && target.OwnerID != caller.OwnerID && target.ChatUser != caller.OwnerID {
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
	if in.Chat = strings.TrimSpace(in.Chat); in.Chat != "" {
		if in.NewChat {
			return nil, ErrChatAndNewChat
		}
		// The chat must be the session person's own, with the target as
		// it is shared with them; never someone else's (read-only) chat.
		rp, ok := h.resumeScope(ctx, in.SessionUser, caller.OwnerID)(target)
		if !ok || !h.ownsChat(ctx, rp, in.Chat) {
			return nil, ErrUnknownChat
		}
		if rp.ChatUser != target.ChatUser {
			// The turn runs in the recipient's chat with the target; the
			// executor re-checks both shares against that person.
			target = rp
			viaShare = true
		}
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
	msg.TaskID = answer
	msg.Metadata = map[string]any{metaFrom: caller.ID, metaDepth: depth, metaSession: in.CallerSession}
	if target.ChatUser != "" {
		msg.Metadata[metaChatUser] = target.ChatUser
	}
	if in.NewChat {
		msg.Metadata[metaNewChat] = true
	}
	if in.Chat != "" {
		msg.Metadata[metaChat] = in.Chat
	}
	if viaShare {
		msg.Metadata[metaSessionUser] = in.SessionUser
	}
	// Human is set only by the person's mention route, never by a tool:
	// the task is theirs (taskRef.origin). Their answer to it stays theirs,
	// down to a new task standing in for one a restart lost.
	userOrigin := in.Human && (answer == "" || claim.origin == OriginUser)
	if userOrigin {
		msg.Metadata[metaOrigin] = OriginUser
	}

	cl, err := h.client(ctx, card)
	if err != nil {
		return nil, fmt.Errorf("team: connect @%s: %w", target.Handle, err)
	}
	req := &a2a.SendMessageRequest{Message: msg, Config: &a2a.SendMessageConfig{ReturnImmediately: true}}
	res, err := cl.SendMessage(ctx, req)
	// An answer that comes right back can find the teammate's last turn
	// still closing its execution of the task: retry for a moment.
	for until := time.Now().Add(answerRetry); err != nil && answer != "" && strings.Contains(err.Error(), "already in progress") && time.Now().Before(until); {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(h.poll):
		}
		res, err = cl.SendMessage(ctx, req)
	}
	note := ""
	var lost a2a.TaskID
	if err != nil && answer != "" && errors.Is(err, a2a.ErrTaskNotFound) {
		// The task store lost the task (wick restarted): the answer goes
		// as a new task of the same exchange, and the lost one is settled
		// once that went through (still claimed until then).
		lost = answer
		msg.TaskID, answer = "", ""
		res, err = cl.SendMessage(ctx, req)
		note = "The asking task was lost when wick restarted; your answer went to the teammate as a new task in the same exchange. "
	}
	if err != nil {
		return nil, fmt.Errorf("team: send to @%s: %w", target.Handle, err)
	}
	task, ok := res.(*a2a.Task)
	if !ok {
		return nil, fmt.Errorf("team: @%s answered without a task", target.Handle)
	}
	// The teammate took the answer: the claim holds until its turn ends.
	claim = nil
	if lost != "" {
		h.supersede(lost, task.ID)
	}

	wait := in.Wait
	h.mu.Lock()
	ref := h.tasks[task.ID]
	if ref == nil {
		ref = &taskRef{}
		h.tasks[task.ID] = ref
	}
	ref.agentID, ref.callerAgentID, ref.callerOwner, ref.callerSession, ref.to = target.ID, caller.ID, caller.OwnerID, in.CallerSession, target
	ref.touched = h.now()
	if ref.started.IsZero() {
		ref.contextID, ref.title, ref.started = contextID, firstLine(in.Text), ref.touched
	}
	if userOrigin {
		ref.origin, ref.originUser = OriginUser, in.SessionUser
	}
	if wait == 0 && h.fanningOutLocked(in.CallerSession, task.ID) {
		wait = -1
	}
	h.mu.Unlock()
	h.persist(task.ID)

	out, err := h.wait(ctx, cl, task.ID, contextID, target, wait)
	if out != nil && note != "" {
		out.Note = note + out.Note
	}
	return out, err
}

// answering checks in.TaskID for an answer to a question: a task this
// caller sent, in in.ContextID when one is given, to in.To when one is
// given, that ended input_required. It returns the teammate's handle and
// the task's context.
// The check and the claim are one critical section: of two answers racing
// for the same question (the user's and the agent's, or two tool calls)
// exactly one gets the claim, the other ErrAlreadyAnswered.
func (h *Hub) answering(callerID string, in SendInput) (string, string, *answerClaim, error) {
	id := a2a.TaskID(in.TaskID)
	h.mu.Lock()
	defer h.mu.Unlock()
	ref := h.tasks[id]
	if ref == nil {
		ref = h.loadLocked(id)
	}
	if ref == nil || ref.callerAgentID != callerID {
		return "", "", nil, h.unknownLocked(callerID, id)
	}
	if ref.userTask() && !in.byUser {
		return "", "", nil, ErrUserTask
	}
	if c := strings.TrimSpace(in.ContextID); c != "" && c != ref.contextID {
		return "", "", nil, ErrTaskContext
	}
	if to := NormalizeHandle(in.To); to != "" && to != ref.to.Handle {
		return "", "", nil, fmt.Errorf("%w (@%s)", ErrTaskOtherAgent, ref.to.Handle)
	}
	if ref.claimed || ref.userAnswer {
		return "", "", nil, ErrAlreadyAnswered
	}
	if !ref.finished || ref.state != a2a.TaskStateInputRequired {
		return "", "", nil, fmt.Errorf("%w (it is %s)", ErrTaskNotWaiting, h.viewState(ref))
	}
	// The task runs again: clear what its last turn left before the
	// executor can report the next one.
	claim := &answerClaim{waiterGone: ref.waiterGone, delivered: ref.delivered, reply: ref.reply, state: ref.state, origin: ref.origin, originUser: ref.originUser}
	ref.claimed, ref.userAnswer = true, in.byUser
	// This process runs it now: not a restart's leftover any more.
	ref.fromDisk = false
	ref.finished, ref.waiterGone, ref.delivered, ref.reply, ref.state = false, false, false, "", a2a.TaskStateWorking
	return ref.to.Handle, ref.contextID, claim, nil
}

// unclaim gives task id's question back after an answer that never
// reached the teammate: only the fields answering changed, and only while
// the claim still stands — a cancel (or a turn) that settled the task
// meanwhile is kept.
func (h *Hub) unclaim(id a2a.TaskID, c *answerClaim) {
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil || !ref.claimed {
		h.mu.Unlock()
		return
	}
	ref.claimed, ref.userAnswer = false, false
	if !ref.canceled {
		ref.finished, ref.waiterGone, ref.delivered, ref.reply, ref.state = true, c.waiterGone, c.delivered, c.reply, c.state
	}
	h.mu.Unlock()
	h.persist(id)
}

// supersede settles task id, whose answer went out as task next because
// the task store had lost id: it neither waits for an answer nor reads
// as answered forever.
func (h *Hub) supersede(id, next a2a.TaskID) {
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil || !ref.claimed || ref.canceled {
		h.mu.Unlock()
		return
	}
	ref.claimed, ref.userAnswer, ref.canceled = false, false, true
	ref.finished, ref.delivered, ref.state, ref.touched = true, true, a2a.TaskStateCanceled, h.now()
	ref.reply = fmt.Sprintf("superseded by task %s: this task was lost when wick restarted, the answer went there", next)
	h.mu.Unlock()
	h.persist(id)
}

// fanningOutLocked reports whether session has another task still
// working that it sent within fanOutWindow.
func (h *Hub) fanningOutLocked(session string, except a2a.TaskID) bool {
	if session == "" {
		return false
	}
	now := h.now()
	for id, t := range h.tasks {
		if id != except && t.callerSession == session && !t.finished && now.Sub(t.started) < fanOutWindow {
			return true
		}
	}
	return false
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
	h.pruneFilesLocked(now)
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

// chatFor is the session target answers contextID in: resume when the
// person asked to continue that chat (re-paired with callerSession), a
// new chat when newChat asks for one, the chat an earlier new_chat opened for this
// exchange, else the chat paired with callerSession (ChatLinker) — opened
// and paired on the first message from that conversation. "" is the main
// chat, where Turns cannot pair.
func (h *Hub) chatFor(ctx context.Context, contextID, callerSession string, target Peer, newChat bool, resume string) (string, error) {
	key := chatKey(target)
	op, canOpen := h.Turns.(ChatOpener)
	lk, canLink := h.Turns.(ChatLinker)
	if newChat && !canOpen {
		return "", ErrNewChatUnsupported
	}
	if resume != "" {
		// Re-checked here, whatever the metadata claims: only one of the
		// target's chats with the caller's side.
		if newChat || !canOpen || !h.ownsChat(ctx, target, resume) {
			return "", ErrUnknownChat
		}
		h.linkMu.Lock()
		if canLink && callerSession != "" {
			_ = lk.Link(ctx, target, resume, callerSession)
		}
		h.linkMu.Unlock()
		h.mu.Lock()
		defer h.mu.Unlock()
		if cs := h.contexts[contextID]; cs != nil {
			if cs.chats == nil {
				cs.chats = map[string]string{}
			}
			cs.chats[key] = resume
		}
		return resume, nil
	}
	if !newChat {
		h.mu.Lock()
		var id string
		if cs := h.contexts[contextID]; cs != nil {
			id = cs.chats[key]
		}
		h.mu.Unlock()
		if id != "" || !canOpen || !canLink || callerSession == "" {
			return id, nil
		}
	}
	h.linkMu.Lock()
	defer h.linkMu.Unlock()
	id := ""
	if !newChat {
		id = lk.LinkedChat(ctx, target, callerSession)
	}
	if id == "" {
		var err error
		if id, err = op.NewChat(ctx, target); err != nil {
			return "", err
		}
		if canLink && callerSession != "" {
			// A failed pair still answers in the new chat; the next
			// message just opens another.
			_ = lk.Link(ctx, target, id, callerSession)
		}
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

// wait polls the task until it settles or the wait runs out. A caller
// that stops waiting — the wait ran out, or its tool call was cut off
// (ctx done) — gets the reply delivered into its conversation instead.
func (h *Hub) wait(ctx context.Context, cl *a2aclient.Client, id a2a.TaskID, contextID string, to Peer, wait time.Duration) (*Result, error) {
	if wait == 0 || wait > h.maxWait {
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
			out := h.resultLocked(id, ref)
			out.ContextID = contextID
			if out.ReplyText != "" {
				ref.markSent(textKey(out.ReplyText))
			}
			chat := ref.chat
			h.mu.Unlock()
			h.persist(id)
			out.Chat = h.chatView(ctx, to, chat)
			return out, nil
		}
		if wait < 0 || !time.Now().Before(deadline) {
			chat := ref.chat
			h.mu.Unlock()
			h.handBack(ctx, id)
			return &Result{
				TaskID: string(id), ContextID: contextID, State: "working", To: "@" + to.Handle,
				Note: "Still working. The reply is delivered into this conversation when it lands — end your turn; do not poll or resend.",
				Chat: h.chatView(ctx, to, chat),
			}, nil
		}
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			h.handBack(ctx, id)
			return nil, ctx.Err()
		case <-time.After(h.poll):
		}
		// Reading through the client keeps the task store authoritative;
		// the ref only carries what the store does not (the reply text).
		if _, err := cl.GetTask(ctx, &a2a.GetTaskRequest{ID: id}); err != nil {
			h.handBack(ctx, id)
			return nil, fmt.Errorf("team: read task %s: %w", id, err)
		}
	}
}

// handBack marks task id's caller as gone: its result is delivered into
// the caller's conversation when it lands — or now, when the task settled
// while the caller was leaving.
func (h *Hub) handBack(ctx context.Context, id a2a.TaskID) {
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil {
		h.mu.Unlock()
		return
	}
	ref.waiterGone = true
	settled := ref.finished && !ref.delivered
	state, reply := ref.state, ref.reply
	h.mu.Unlock()
	if settled {
		h.finished(ctx, id, state, reply)
		return
	}
	h.persist(id)
}

// resultLocked is what get_task and a finished wait report for ref.
func (h *Hub) resultLocked(id a2a.TaskID, ref *taskRef) *Result {
	out := &Result{TaskID: string(id), ContextID: ref.contextID, State: h.viewState(ref), To: "@" + ref.to.Handle}
	switch out.State {
	case "failed", "rejected", "canceled":
		out.Reason = ref.reply
		if out.Reason == "" {
			out.Reason = "the teammate's turn ended as " + out.State + " without a reason"
		}
	case "input_required":
		out.ReplyText = ref.reply
		out.Note = "The teammate asks you something. Answer with message task_id=" + string(id) + "; ask the user first if only they can answer."
		if ref.userTask() {
			out.Note = "The teammate asks the user, who sent this task with an @mention: only the user answers it, from the chat. Do not answer it yourself."
		}
	default:
		out.ReplyText = ref.reply
	}
	return out
}

// viewState is the state a caller sees for ref: working until the turn
// ends.
func (h *Hub) viewState(ref *taskRef) string {
	if !ref.finished {
		return "working"
	}
	return stateName(ref.state)
}

// GetTask reports a task the caller sent, from memory or, for an older
// task or one finished by another wick process, from disk.
func (h *Hub) GetTask(ctx context.Context, callerAgentID, taskID string) (*Result, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil || !ref.finished {
		ref = h.loadLocked(id)
	}
	if ref == nil || ref.callerAgentID != callerAgentID || callerAgentID == "" {
		err := h.unknownLocked(callerAgentID, id)
		h.mu.Unlock()
		return nil, err
	}
	out := h.resultLocked(id, ref)
	agentID, to, chat, finished := ref.agentID, ref.to, ref.chat, ref.finished
	h.mu.Unlock()
	out.Chat = h.chatView(ctx, to, chat)
	if finished {
		return out, nil
	}
	// Still running: the task store has the live state and any message
	// the turn has posted so far. A store that lost the task (restart)
	// leaves the record's answer.
	if t, err := h.handler(agentID).GetTask(ctx, &a2a.GetTaskRequest{ID: id}); err == nil {
		if st := stateName(t.Status.State); st != "completed" {
			out.State = st
		}
		if t.Status.Message != nil {
			out.ReplyText = messageText(t.Status.Message)
		}
	}
	return out, nil
}

// CancelTask ends a task the caller sent that is still working or waiting
// for input (A2A CancelTask): the task becomes canceled, the teammate's
// turn for it is stopped — or the task dropped while it still waits for
// one — when Turns can (TaskStopper), and whatever that turn still
// reports is dropped. A turn of anything else in the teammate's session
// is never stopped, and the reason says when one was left running; but a
// message typed into the task's own turn, on a provider that does not
// queue it apart, is stopped with that turn (see TaskStopShared).
// A task the person started (taskRef.origin) is theirs to cancel:
// ErrUserTask.
func (h *Hub) CancelTask(ctx context.Context, callerAgentID, taskID string) (*Result, error) {
	return h.cancelTask(ctx, callerAgentID, taskID, false)
}

// cancelTask is CancelTask; byUser is the person's cancel (CancelFromUser).
func (h *Hub) cancelTask(ctx context.Context, callerAgentID, taskID string, byUser bool) (*Result, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	by := "the sender"
	if byUser {
		by = "the user"
	} else if p, err := h.Dir.Get(ctx, callerAgentID); err == nil {
		by = "@" + p.Handle
	}
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil {
		ref = h.loadLocked(id)
	}
	if ref == nil || ref.callerAgentID != callerAgentID || callerAgentID == "" {
		err := h.unknownLocked(callerAgentID, id)
		h.mu.Unlock()
		return nil, err
	}
	if ref.userTask() && !byUser {
		h.mu.Unlock()
		return nil, ErrUserTask
	}
	if ref.finished && ref.state != a2a.TaskStateInputRequired {
		state := h.viewState(ref)
		h.mu.Unlock()
		return nil, fmt.Errorf("%w (it is %s)", ErrTaskSettled, state)
	}
	running, isRunning := h.inflight[ref.agentID][id]
	reason := "canceled by " + by
	ref.canceled, ref.finished, ref.delivered = true, true, true
	ref.claimed, ref.userAnswer = false, false
	ref.state, ref.reply, ref.touched = a2a.TaskStateCanceled, reason, h.now()
	agentID, to := ref.agentID, ref.to
	h.mu.Unlock()

	if ts, ok := h.Turns.(TaskStopper); ok && isRunning && running.session != "" {
		stop, _ := ts.StopTask(ctx, to, running.session, id, by)
		switch stop {
		case TaskStopRunning:
			reason += stoppedNote
		case TaskStopQueued:
			reason += " before it started"
		case TaskStopBehindOther:
			reason += " before it started; the teammate's current turn was left running because it belongs to another conversation"
		case TaskStopShared:
			reason += "; the teammate's turn was left running because a message from another conversation is queued in it"
		}
		h.mu.Lock()
		ref.reply = reason
		h.mu.Unlock()
	}
	h.persist(id)
	// The task store takes the cancellation once the execution lets go of
	// the task, which can outlast this call: the Hub already says canceled.
	go func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelWait)
		defer cancel()
		_, _ = h.handler(agentID).CancelTask(cctx, &a2a.CancelTaskRequest{ID: id})
	}()
	return h.GetTask(ctx, callerAgentID, string(id))
}

// stoppedNote ends the reason of a cancel that stopped the teammate's
// turn: on a provider that appends messages to the running turn, what
// someone typed into it meanwhile went with it.
const stoppedNote = "; the teammate's current turn was stopped, with anything typed into it while it ran"

// finished is the executor telling the Hub a task's turn ended. A caller
// that stopped waiting gets the reply (or the question, or the reason)
// delivered into its session.
func (h *Hub) finished(ctx context.Context, id a2a.TaskID, state a2a.TaskState, reply string) {
	h.settle(ctx, id, state, reply, false)
}

// settle is finished; orphan settles the task only while it is still a
// restart's leftover (orphanLocked), marking it interrupted.
func (h *Hub) settle(ctx context.Context, id a2a.TaskID, state a2a.TaskState, reply string, orphan bool) {
	h.mu.Lock()
	ref := h.tasks[id]
	if orphan && (ref == nil || !h.orphanLocked(id, ref)) {
		// Answered, canceled, or finished by the draining process since.
		h.mu.Unlock()
		return
	}
	if ref == nil {
		// The executor can finish before Send registered the task.
		ref = &taskRef{}
		h.tasks[id] = ref
	}
	if ref.canceled {
		// cancel_task already settled it and told the caller.
		h.mu.Unlock()
		return
	}
	ref.interrupted = orphan
	ref.finished, ref.state, ref.reply, ref.touched = true, state, reply, h.now()
	ref.claimed, ref.userAnswer = false, false
	deliver := ref.waiterGone && !ref.delivered && ref.callerSession != ""
	if ref.userTask() {
		// The person's task: the reply or question shows on the task in
		// their chat (OnTaskChange), never as a prompt waking its agent.
		ref.delivered, deliver = true, false
	}
	if deliver {
		ref.delivered = true
		if reply != "" {
			ref.markSent(textKey(reply))
		}
	}
	session, to := ref.callerSession, ref.to
	h.mu.Unlock()
	h.persist(id)
	if !deliver || h.Notify == nil {
		return
	}
	if state == a2a.TaskStateCompleted && reply == "" {
		return // [silent]: nothing to hand back
	}
	text := fmt.Sprintf("Reply from %s [task %s, %s]:\n\n%s", to.Label(), id, stateName(state), reply)
	if state == a2a.TaskStateInputRequired {
		text += fmt.Sprintf("\n\n(%s asks you this. Answer with team_message task_id=%s; ask the user first if only they can answer.)", to.Label(), id)
	}
	if err := h.Notify.Deliver(context.WithoutCancel(ctx), session, text); err != nil {
		_ = err // best-effort: the task store still holds the result
	}
}

// FollowUp hands text — a reply the agent answering in sessionID sent
// after its task had ended, such as a remote agent's late message — to
// whoever asked, delivered into the asker's session like a late reply.
// A text the asker already has for that task (the same answer posted
// again) is dropped and still counts as handled. False when the session
// answered no task the Hub still remembers.
func (h *Hub) FollowUp(ctx context.Context, sessionID, text string) bool {
	if sessionID == "" || strings.TrimSpace(text) == "" || h.Notify == nil {
		return false
	}
	h.mu.Lock()
	id, ok := h.answered[sessionID]
	ref := h.tasks[id]
	if ok && ref == nil {
		ref = h.loadLocked(id)
	}
	if ok && ref != nil && ref.canceled {
		// The sender canceled the task: what its turn still says is not
		// theirs to read any more.
		h.mu.Unlock()
		return true
	}
	var session string
	var to Peer
	dup, userTask := false, false
	if ok && ref != nil {
		session, to, userTask = ref.callerSession, ref.to, ref.userTask()
		if dup = ref.alreadySent(text); !dup {
			ref.markSent(textKey(text))
			if userTask {
				// The person's task: the follow-up shows on it, without
				// waking the agent of the chat. It joins a reply; a late
				// reply to a turn that failed (a remote's timeout) is the
				// reply. A question or an unfinished turn keeps its text.
				switch {
				case !ref.finished:
				case ref.state == a2a.TaskStateCompleted:
					ref.reply = strings.TrimSpace(ref.reply + "\n\n" + text)
					ref.touched = h.now()
				case ref.state == a2a.TaskStateFailed:
					ref.state, ref.reply, ref.interrupted = a2a.TaskStateCompleted, strings.TrimSpace(text), false
					ref.touched = h.now()
				}
			}
		}
	}
	h.mu.Unlock()
	if session == "" {
		return false
	}
	if dup {
		return true
	}
	h.persist(id)
	if userTask {
		return true
	}
	msg := fmt.Sprintf("Follow-up from %s [task %s]:\n\n%s", to.Label(), id, text)
	return h.Notify.Deliver(context.WithoutCancel(ctx), session, msg) == nil
}

// TaskView is one task a session sent, as the Sub-agents panel and
// list_tasks show it.
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
	// Age is how long ago the task was sent, rounded ("3m", "2h").
	Age string `json:"age"`
	// Summary is the first line of the reply, question or reason.
	Summary string `json:"summary,omitempty"`
	// Reply is the reply, question or reason itself, capped, for the
	// thread's preview of the task.
	Reply string `json:"reply,omitempty"`
	// ChatID is the teammate's chat the task runs in ("" = main or not
	// known yet), so the UI can open it.
	ChatID string `json:"chat_id,omitempty"`
	// NeedsYou: the task asks a question (input_required) that no turn of
	// the sending conversation is handling, so it waits for the user.
	NeedsYou bool `json:"needs_you,omitempty"`
	// Interrupted: wick restarted while the task was working, so it
	// failed (settleOrphans); sending it again is safe.
	Interrupted bool `json:"interrupted,omitempty"`
	// Origin is OriginUser for a task the person sent with an @mention
	// in this chat ("" = the agent's): its question always waits for the
	// person, whatever the agent is doing.
	Origin string `json:"origin,omitempty"`
}

// SentFrom lists the tasks sessionID sent, newest first: those in memory
// and those only on disk (older, or from before a restart). Unfinished
// tasks read "working".
func (h *Hub) SentFrom(sessionID string) []TaskView {
	if sessionID == "" {
		return nil
	}
	// Read before taking the lock: the pool is none of the Hub's.
	busy := h.CallerBusy != nil && h.CallerBusy(sessionID)
	// The files are read without h.mu, which every Send and turn needs:
	// snapshot what memory already settled, read, then adopt under it.
	h.mu.Lock()
	h.pruneLocked()
	dir, skip := h.dir, map[a2a.TaskID]bool{}
	if dir != "" {
		for id, ref := range h.tasks {
			if ref.finished {
				skip[id] = true
			}
		}
	}
	h.mu.Unlock()
	recs := records(dir, sessionID, skip)
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range recs {
		h.adoptLocked(rec, false)
	}
	now := h.now()
	out := []TaskView{}
	orphans := false
	for id, ref := range h.tasks {
		if ref.callerSession != sessionID || ref.started.IsZero() {
			continue
		}
		orphans = orphans || h.orphanLocked(id, ref)
		v := h.viewLocked(id, ref, now)
		v.NeedsYou = needsYou(v, ref.userAnswer, busy)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	if orphans {
		// A task a restart left working: settle it now rather than on the
		// next sweep; the change reaches the list through OnTaskChange.
		go h.settleOrphans(context.Background())
	}
	return out
}

// viewLocked is task id as the UI lists it, NeedsYou left to the caller
// (it reads the pool, which h.mu must not wrap).
func (h *Hub) viewLocked(id a2a.TaskID, ref *taskRef, now time.Time) TaskView {
	turns, limit := 0, MaxContextTurns
	if c := h.contexts[ref.contextID]; c != nil {
		turns, limit = c.turns, c.limit
	}
	v := TaskView{
		TaskID: string(id), ContextID: ref.contextID,
		ToID: ref.to.ID, ToHandle: ref.to.Handle, ToName: ref.to.Name,
		Title: ref.title, State: h.viewState(ref), Turns: turns, MaxTurns: limit,
		Started: ref.started, Updated: ref.touched,
		Age: shortAge(now.Sub(ref.started)),
	}
	if ref.finished {
		v.Summary, v.Reply = firstLine(ref.reply), capText(ref.reply, replyPreviewMax)
	}
	v.ChatID = ref.chat
	v.Interrupted = ref.interrupted
	v.Origin = ref.origin
	return v
}

// needsYou is TaskView.NeedsYou for v: a question nobody is answering
// yet, waiting for the person — always for their own task, otherwise
// only while no turn of the sending chat (busy) may answer it.
func needsYou(v TaskView, userAnswer, busy bool) bool {
	return v.State == "input_required" && !userAnswer && (v.Origin == OriginUser || !busy)
}

// shortAge rounds d for a list: seconds under a minute, then minutes,
// hours, days.
func shortAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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

// stateName maps an A2A state to the word the tool reports: working,
// input_required, completed, failed, canceled or rejected. submitted
// (and an unset state) read working; auth_required reads rejected — the
// caller cannot authenticate for the teammate, only its owner can.
func stateName(s a2a.TaskState) string {
	switch s {
	case a2a.TaskStateUnspecified, a2a.TaskStateSubmitted, a2a.TaskStateWorking:
		return "working"
	case a2a.TaskStateAuthRequired:
		return "rejected"
	}
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
