package delegation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// childSegRunes is how much of the delegation UUID names the child's
// folder. Twelve hex chars keep collisions out of reach within one
// parent while keeping a deep session's trace path inside the Windows
// path limit — nesting with full UUIDs overflows it around depth 2.
const childSegRunes = 12

// childSessionIDFor derives a sub-agent's session ID from its parent and
// the delegation that spawned it.
//
// The ID carries the parent's, so config.Layout can resolve the child's
// nested folder without consulting anything. Deriving the segment from
// the delegation UUID (rather than a fresh random) means a retried
// dispatch of the same delegation lands on the same folder instead of
// leaving an abandoned transcript behind.
//
// A delegation with no parent session — nothing dispatches one today,
// but the request shape allows it — falls back to the flat `sub-<uuid>`
// form rather than producing an ID that starts with the separator.
func childSessionIDFor(parentSessionID, delegationID string) string {
	seg := strings.ReplaceAll(delegationID, "-", "")
	if len(seg) > childSegRunes {
		seg = seg[:childSegRunes]
	}
	if parentSessionID == "" {
		return "sub-" + delegationID
	}
	return agentconfig.SubSessionID(parentSessionID, seg)
}

// EventStream is the subset of the SSE broadcaster this package needs:
// subscribe to one session's normalized agent events.
//
// Declared as a local interface rather than importing the broadcaster so
// delegation stays free of the HTTP/tools layer (and out of an import
// cycle). tools/agents.Broadcaster satisfies it structurally via the
// adapter in the wiring.
type EventStream interface {
	SubscribeSession(sessionID string) (<-chan StreamEvent, func())
}

// StreamEvent is one normalized agent event, flattened to what the turn
// counter and partial-text capture actually read.
type StreamEvent struct {
	Type event.EventType
	Text string
	// Raw is the verbatim provider line, when available. Token usage is
	// read from here because the normalized event carries no usage field
	// and providers report it in their own shapes.
	Raw string
}

// Runner drives one session as a sub-agent. Implemented by the pool
// wiring; an interface here keeps this package testable without a live
// subprocess.
type Runner interface {
	// EnsureChildSession creates the isolated session for a delegation,
	// linked to parentSessionID and owned by userID.
	EnsureChildSession(ctx context.Context, childSessionID, parentSessionID, projectID, userID string) error
	// StartAgent spawns the profile's provider into the child session
	// with its system prompt and scoped MCP identity, then delivers task
	// as the first user message.
	StartAgent(ctx context.Context, spec ChildSpec) error
	// KillAgent stops one agent inside a session.
	KillAgent(sessionID, agentName string) error
	// PartialText returns whatever the child has produced so far,
	// including an in-flight turn that has not been flushed by Done.
	PartialText(sessionID, agentName string) string
}

// ChildSpec is everything needed to spawn one sub-agent.
type ChildSpec struct {
	SessionID string
	AgentName string
	Profile   *entity.AgentProfile
	// Target is the resolved provider instance + model this child runs on
	// (see resolveChildTarget). Already carries the role's own pin, or the
	// parent conversation's, or the project's — the runner must use this
	// rather than re-reading Profile.Provider/Model, which is only the
	// first candidate in that chain.
	//
	// Empty Provider means nothing in the chain named one; the spawn path
	// then applies its own per-type default.
	Target  provider.RunTarget
	Task    string
	Context string
	// MCPToken is the SCOPED token minted for this child. It
	// authenticates as the triggering human with an already-narrowed tag
	// set. It must never be the global internal token, which would make
	// the child an admin and silently void the profile's tag limits.
	MCPToken string
	// TagIDs is the effective tag set, passed through for logging and
	// for spawners that filter tool exposure at argv level.
	TagIDs []string
	// MaxTurns is the resolved per-child cap, already clamped.
	MaxTurns int
	// MaxTokens is the resolved per-child token cap. 0 = uncapped at the
	// delegation level.
	MaxTokens int
	// WorkspacePath is the isolated directory to run in (Phase 3).
	// Empty = the project's shared workspace.
	WorkspacePath string
}

// TokenIssuer mints and revokes scoped MCP identities.
type TokenIssuer interface {
	Issue(userID string, tagIDs []string) (string, error)
	Revoke(token string)
}

// WorkspaceProvider prepares an isolated working directory for a
// sub-agent (Phase 3).
//
// PrepareWorktree returns the path to use, plus an optional note when the
// request could not be honoured exactly. An empty path means "no
// isolation was possible, use the shared workspace" — expected on
// non-git projects, and reported rather than hidden.
type WorkspaceProvider interface {
	PrepareWorktree(ctx context.Context, projectID, childSessionID string) (path, note string, err error)
	// ReleaseWorktree cleans up after a delegation finishes. Best-effort:
	// a failure here must never fail the delegation itself.
	ReleaseWorktree(ctx context.Context, path string) error
}

// ResultDeliverer routes an async delegation's result to its sink
// (Phase 2/4). Implemented by the wiring, which knows about channels and
// the pool; kept behind an interface so this package stays free of them.
type ResultDeliverer interface {
	// DeliverToChannel posts a finished result back to the conversation
	// that triggered it.
	DeliverToChannel(ctx context.Context, parentSessionID, text string) error
	// DeliverToSession re-prompts the leader with the result, so an async
	// delegation can wake the agent that fired it.
	DeliverToSession(ctx context.Context, parentSessionID, agentName, text string) error
}

// Service performs delegations. All the collaborators are interfaces so
// the governor and lifecycle logic can be tested without spawning real
// processes.
type Service struct {
	Repo   *Repo
	Runner Runner
	Stream EventStream
	Tokens TokenIssuer
	Tags   TagResolver
	// Workspaces prepares isolated worktrees (Phase 3). nil = every
	// delegation runs in the shared workspace.
	Workspaces WorkspaceProvider
	// Deliver routes async results (Phase 2/4). nil = async results are
	// recorded but not pushed anywhere.
	Deliver ResultDeliverer
	// Steerer delivers human take-over messages into a running sub-agent.
	// nil = take-over is unavailable on this transport.
	Steerer Steerer
	// Waker resumes an exited sub-agent so it can read its inbox.
	// nil = messages queue but nobody is woken to read them.
	Waker Waker
	// AgentAlive reports whether a child still has a live process in the
	// pool. Read by the sweeper to tell a slow sub-agent from one that has
	// exited without its run ever being closed.
	//
	// nil = the sweep is skipped entirely. A wiring that cannot answer
	// must never guess an agent is dead: finishing a delegation that is
	// actually still working would throw away the run.
	AgentAlive func(childSessionID, agentName string) bool
	// Resumable reports whether a child's provider transcript can still be
	// picked up — i.e. whether the next spawn will carry --resume rather
	// than start blank. Read by Continue so a leader is told when its
	// sub-agent woke up without its memory.
	//
	// nil = assume resumable. A wiring that cannot answer must not make
	// every continuation announce a memory loss it has no evidence for.
	Resumable func(childSessionID, agentName string) bool
	// AskTimeout bounds a blocking ask. 0 = defaultAskTimeout.
	AskTimeout time.Duration
	// InboxCap bounds undelivered messages per handle. 0 = defaultInboxCap.
	InboxCap int
	// Limits is the fallback ceiling set, used when LimitsFn is nil.
	Limits Limits
	// LimitsFn, when set, is consulted on EVERY delegation so config
	// changes — above all the kill-switch — take effect immediately
	// rather than at the next restart.
	LimitsFn func() Limits
	// Secrets yields the values that must never reach a customer-facing
	// draft. nil = no value masking, and the drafter's prompt is then the
	// only thing standing between an injection and a leaked token.
	Secrets func(ctx context.Context, projectID string) []string
	// ProjectRoot resolves a project's working directory, so absolute
	// paths can be reduced to filenames in a draft. nil = paths are left
	// as written.
	ProjectRoot func(ctx context.Context, projectID string) string
	// SessionTarget reports which provider+model a session is running on,
	// so a sub-agent whose role names none inherits its parent's rather
	// than jumping to a global default. nil = no session inheritance.
	SessionTarget SessionRunTarget
	// ProjectTarget reports a project's default provider+model, the last
	// step of the inheritance chain. nil = no project inheritance.
	ProjectTarget ProjectRunTarget
	// OnDetachedChange is called with the CURRENT list of still-running
	// detached sub-agents under a parent session whenever one of them ends.
	//
	// Killing a leader spares its detached children by design, and a channel
	// may be showing a notice that says so. Without this the notice would
	// keep claiming work is in progress after it finished. Passing the whole
	// list rather than a delta lets the consumer keep one message and edit it.
	// nil = nobody is showing such a notice.
	OnDetachedChange func(parentSessionID string, survivors []Survivor)

	// mu guards inflight and slotWaiters.
	mu sync.Mutex
	// inflight maps delegation id → cancel func, so Interrupt can
	// unblock a Run that is waiting on its child.
	inflight map[string]context.CancelFunc
	// slotWaiters maps root id → callers parked in waitForSlot. Woken by
	// pokeSlot when a delegation in that tree reaches a terminal status.
	slotWaiters map[string][]chan struct{}
}

// Request is one wick_delegate call.
type Request struct {
	ProfileKey string
	Task       string
	Context    string
	MaxTurns   int
	// MaxTokens caps spend for this one delegation. 0 = fall back to the
	// profile default, then to uncapped-by-the-call.
	MaxTokens int
	// Mode is "sync" (block, return the answer) or "async" (return a
	// handle immediately and keep running). Empty = the profile default.
	Mode string
	// DeliverySink routes an async result: channel | session | none.
	DeliverySink string
	// Workspace is "shared" or "worktree". Empty = the profile default.
	Workspace string
	// MemoryMode decides what the sub-agent is told about the rest of the
	// conversation. Empty = the profile default, then state_summary.
	MemoryMode string
	// Supervised asks this sub-agent to file progress notes as it works,
	// which wake the delegating agent. Off by default: a short task should
	// not pay for reporting nobody is watching for.
	Supervised bool
	// SquadKey scopes which profiles the resulting sub-agent may reach.
	SquadKey string
	// TaskID links this delegation back to a board task, when one drove it.
	TaskID string

	// ParentSessionID is the leader's session.
	ParentSessionID string
	ParentAgent     string
	// TriggeredBy is the HUMAN at the root of the tree — not the leader
	// agent. Drives tag inheritance and interrupt authorization.
	TriggeredBy string
	ProjectID   string
	// RootID / Depth / AncestorKeys describe where this call sits in the
	// tree. Depth 0 with an empty RootID means a fresh root.
	RootID       string
	Depth        int
	AncestorKeys []string
}

// Result is what the leader receives back as a tool result.
type Result struct {
	DelegationID string `json:"delegation_id"`
	Profile      string `json:"profile"`
	Status       string `json:"status"`
	TurnsUsed    int    `json:"turns_used"`
	TokensUsed   int    `json:"tokens_used,omitempty"`
	Result       string `json:"result"`
	Note         string `json:"note,omitempty"`
	// Mode echoes how this ran, in the spoken names: "background" or
	// "foreground" (ModeLabel). For a background run it is the signal that
	// Result is intentionally empty and the answer arrives later.
	Mode string `json:"mode,omitempty"`
	// QueuePosition is the 1-based place in this conversation's queue for
	// a delegation that has not started yet. 0 for anything already
	// running — a leader reading "queued #3" knows its fan-out is lined
	// up rather than all reading at once.
	QueuePosition int `json:"queue_position,omitempty"`
	// UserSteered marks a result a human intervened in mid-flight, so the
	// leader does not present it as the role's own unaided work.
	UserSteered bool `json:"user_steered,omitempty"`
	// WorkspaceNote explains a downgraded workspace mode (e.g. worktree
	// requested on a non-git project).
	WorkspaceNote string `json:"workspace_note,omitempty"`
	// Envelope is the sub-agent's answer as typed fields. Additive: Result
	// still carries the prose, so a caller that ignores this sees no
	// change. `structured: false` means the sub-agent never called
	// report_result and this was reconstructed from its closing message.
	Envelope *ResultEnvelope `json:"envelope,omitempty"`
	// Continued marks a result produced by Continue rather than by a fresh
	// delegation — the same sub-agent, in the session it already had.
	Continued bool `json:"continued,omitempty"`
	// Resumed reports whether that continuation actually recovered the
	// sub-agent's earlier transcript. Only meaningful with Continued.
	// False means the session was reused but the memory was not, which the
	// leader must know before writing a follow-up that refers back to work
	// the sub-agent can no longer see.
	Resumed bool `json:"resumed,omitempty"`
}

// interruptNote is the exact text handed to a leader whose sub-agent a
// human stopped. The final sentence is load-bearing: without an explicit
// instruction not to, models routinely re-issue the same delegation
// immediately, which is the opposite of what the person who clicked Stop
// wanted.
const interruptNote = "Interrupted by the user. Decide: continue without it, re-delegate, or ask the user. Do NOT silently retry."

// Run executes one delegation synchronously and returns the child's
// final output.
//
// It never returns an error for a stopped or exhausted child. Those come
// back as a Result carrying partial work plus a status the leader can
// reason about, because a tool error reads to the model as "that call
// failed, try again" — precisely the wrong reaction to a human pressing
// Stop. Genuine infrastructure failures (no such profile, refused by the
// governor, spawn failed) still return errors.
func (s *Service) Run(ctx context.Context, req Request) (*Result, error) {
	profile, err := s.Repo.GetProfileScoped(ctx, req.ProjectID, req.ProfileKey)
	if err != nil {
		return nil, err
	}

	limits := s.limits()
	depth := req.Depth
	rootID := req.RootID
	if err := s.Repo.Admit(ctx, limits, profile, rootID, depth, req.AncestorKeys); err != nil {
		return nil, err
	}

	// Tag inheritance: start from the triggering human's tags, then let
	// the profile narrow. Never the other way around.
	userTags := []string(nil)
	if s.Tags != nil && req.TriggeredBy != "" {
		userTags = s.Tags.GetUserFilterTagIDs(ctx, req.TriggeredBy)
	}
	effTags := EffectiveTags(userTags, profile)

	id := uuid.NewString()
	if rootID == "" {
		rootID = id
	}
	childSessionID := childSessionIDFor(req.ParentSessionID, id)
	// The agent entry's name inside the child session.
	//
	// The role KEY, not its provider. Naming it after the provider produced an
	// entry called "" for every role that names no provider — and an unnamed
	// entry is unaddressable: SetModelID and SetMaxTurns create a row when the
	// name does not match, so each spawn appended another blank one instead of
	// updating the real agent. It also read as a second agent in the session's
	// own agents.json. The key is stable, unique within a scope, and already
	// what the rest of the tree addresses this role by.
	agentName := strings.TrimSpace(profile.Key)
	if agentName == "" {
		// A profile with no key cannot happen through the API (it is
		// required), but a blank name is exactly the bug above, so refuse
		// rather than write one.
		return nil, errors.New("delegation: profile has no key to name its agent entry")
	}
	maxTurns := EffectiveMaxTurns(req.MaxTurns, profile, limits)
	maxTokens := EffectiveMaxTokens(req.MaxTokens, profile, limits)

	mode := NormalizeMode(req.Mode, profile.DefaultMode)
	sink := req.DeliverySink
	if !ValidSink(sink) {
		return nil, fmt.Errorf("unknown delivery_sink %q", sink)
	}
	// An async delegation with nowhere to deliver would run, cost money,
	// and hand its answer to nobody. Default it to waking the leader:
	// async is the default mode now, so the common case is a leader that
	// dispatched work and still has the next step to take once it lands.
	// SinkChannel posts the raw result next to the conversation and leaves
	// nobody to act on it, which reads as the flow stalling.
	if mode == ModeAsync && sink == "" {
		sink = SinkSession
	}

	// Workspace: resolve the mode, then let the runner tell us whether it
	// could honour it. A downgrade is recorded, never silent.
	workspace := NormalizeWorkspace(req.Workspace, profile.DefaultWorkspace)
	workspacePath, workspaceNote := "", ""
	if workspace == WorkspaceWorktree && s.Workspaces != nil {
		path, note, werr := s.Workspaces.PrepareWorktree(ctx, req.ProjectID, childSessionID)
		if werr != nil {
			// Failing to isolate is not a reason to refuse the work: fall
			// back to the shared directory and say so.
			workspace, workspaceNote = WorkspaceShared, "worktree unavailable ("+werr.Error()+"); ran in the shared workspace"
		} else {
			workspacePath, workspaceNote = path, note
			if path == "" {
				workspace = WorkspaceShared
			}
		}
	}

	// Address this instance inside its tree. Allocated here, before the
	// row exists, so every delegation is reachable from the moment it is
	// recorded — a sub-agent that spawns without a handle can be spoken
	// about but never spoken to.
	takenHandles, err := s.Repo.TakenHandles(ctx, rootID)
	if err != nil {
		return nil, fmt.Errorf("resolve handles: %w", err)
	}

	now := time.Now().UTC()
	row := &entity.AgentDelegation{
		Handle:          AllocateHandle(profile.Key, takenHandles),
		ID:              id,
		RootID:          rootID,
		ParentSessionID: req.ParentSessionID,
		ParentAgent:     req.ParentAgent,
		ProfileKey:      profile.Key,
		ChildSessionID:  childSessionID,
		ChildAgent:      agentName,
		Task:            req.Task,
		Depth:           depth,
		Mode:            mode,
		DeliverySink:    sink,
		// An async sub-agent is detached by design: it was fired to run on
		// its own, so leader teardown must not take it with it.
		Detached:      mode == ModeAsync,
		AncestorKeys:  encodeStringSlice(append(append([]string{}, req.AncestorKeys...), profile.Key)),
		ProjectID:     req.ProjectID,
		Workspace:     workspace,
		WorkspacePath: workspacePath,
		WorkspaceNote: workspaceNote,
		SquadKey:      req.SquadKey,
		Status:        entity.DelegationQueued,
		MaxTurns:      maxTurns,
		MaxTokens:     maxTokens,
		TriggeredBy:   req.TriggeredBy,
		StartedAt:     now,
		// Preserved so a row that waits in the queue can be started later
		// with exactly the background its caller supplied.
		ContextText: req.Context,
		MemoryMode:  NormalizeMemoryMode(req.MemoryMode, profile.DefaultMemoryMode),
		Supervised:  req.Supervised,
	}
	// Stamp the investigation round this delegation belongs to, so "is
	// the round finished?" stays a query. One indexed lookup against a
	// process spawn is not worth avoiding, and it returns nothing for the
	// ordinary trees that have no incident.
	if inc, ierr := s.IncidentForRoot(ctx, rootID); ierr == nil && inc != nil {
		row.IncidentID = inc.ID
		row.Iteration = inc.Iteration
	}

	if err := s.Repo.Create(ctx, row); err != nil {
		return nil, err
	}

	// A parent blocked on a synchronous child is waiting, not working, and
	// must release its slot for the duration. Without this a serial room
	// deadlocks the moment a sub-agent delegates: the parent holds the only
	// slot until a child that can never start finishes.
	if mode == ModeSync {
		if parent, perr := s.Repo.FindByChildSession(ctx, req.ParentSessionID); perr == nil && parent != nil {
			if berr := s.Repo.SetBlocked(ctx, parent.ID, true); berr == nil {
				defer func() {
					// WithoutCancel: a cancelled caller must still release
					// the slot, or the tree wedges on a phantom holder.
					if cerr := s.Repo.SetBlocked(context.WithoutCancel(ctx), parent.ID, false); cerr != nil {
						log.Warn().Err(cerr).Str("delegation", parent.ID).
							Msg("delegation: unblock failed; the sweeper will clear it")
					}
					s.pokeSlot(rootID)
				}()
			}
		}
	}

	// Wait our turn. The room runs one sub-agent at a time by default.
	if !s.hasSlot(ctx, rootID) {
		if mode == ModeAsync {
			pos, _ := s.Repo.QueuePosition(ctx, rootID, id)
			return queuedResult(row, pos), nil
		}
		if werr := s.waitForSlot(ctx, rootID, id); werr != nil {
			s.finish(ctx, row, entity.DelegationQueued, entity.DelegationInterrupted,
				"", "the caller went away while this was queued", 0)
			return &Result{
				DelegationID: id, Profile: profile.Key,
				Status: entity.DelegationInterrupted, Mode: ModeForeground,
				Note: "Cancelled before it started — the caller went away while this was queued.",
			}, nil
		}
	}

	if err := s.Repo.MarkRunning(ctx, id); err != nil {
		log.Warn().Err(err).Str("delegation", id).Msg("delegation: mark running failed")
	}
	row.Status = entity.DelegationRunning

	return s.execute(ctx, row, profile, effTags)
}

// execute spawns and drives a delegation whose row already exists, is
// already marked running, and already holds its parallel slot.
//
// Split out of Run so the queue dispatcher can start work nobody is
// blocking on: everything it needs is on the row, so a delegation started
// two minutes after it was requested runs exactly as it was asked for.
func (s *Service) execute(
	ctx context.Context,
	row *entity.AgentDelegation,
	profile *entity.AgentProfile,
	effTags []string,
) (*Result, error) {
	id := row.ID
	mode := row.Mode
	sink := row.DeliverySink
	childSessionID := row.ChildSessionID
	workspacePath := row.WorkspacePath
	workspaceNote := row.WorkspaceNote

	// An async delegation outlives the request that started it, so its
	// work must not hang off that request's context — the HTTP handler
	// returning would cancel the child mid-run. Detach the lifetime, but
	// keep the values (logger, request id) for traceable logs.
	baseCtx := ctx
	if mode == ModeAsync {
		baseCtx = context.WithoutCancel(ctx)
	}

	// Mint the scoped identity AFTER the row exists so a token can never
	// outlive an unrecorded run. It is revoked when the run ends — for
	// async that happens on the background goroutine, not here, or the
	// child would lose its credential the moment this call returned.
	var token string
	if s.Tokens != nil && row.TriggeredBy != "" {
		issued, err := s.Tokens.Issue(row.TriggeredBy, effTags)
		if err != nil {
			s.finish(ctx, row, entity.DelegationRunning, entity.DelegationFailed, "", "mint scoped token: "+err.Error(), 0)
			return nil, err
		}
		token = issued
	}
	revoke := func() {
		if token != "" && s.Tokens != nil {
			s.Tokens.Revoke(token)
		}
	}

	if err := s.Runner.EnsureChildSession(baseCtx, childSessionID, row.ParentSessionID, row.ProjectID, row.TriggeredBy); err != nil {
		revoke()
		s.finish(ctx, row, entity.DelegationRunning, entity.DelegationFailed, "", "create child session: "+err.Error(), 0)
		return nil, err
	}

	// Subscribe BEFORE starting the agent. Subscribing afterwards races
	// a fast child, which can emit its whole turn — including Done —
	// before the listener attaches, leaving the waiter hanging forever.
	var ch <-chan StreamEvent
	unsub := func() {}
	if s.Stream != nil {
		ch, unsub = s.Stream.SubscribeSession(childSessionID)
	}

	runCtx, cancel := context.WithCancel(baseCtx)
	s.trackInflight(id, cancel)

	// Resolved here rather than in the runner: the parent session and the
	// project are known at this level, and the runner should be handed a
	// decision rather than the inputs to one.
	target := resolveChildTarget(profile, row.ParentSessionID, row.ProjectID, s.SessionTarget, s.ProjectTarget)
	log.Debug().
		Str("delegation", id).
		Str("child_session", childSessionID).
		Str("provider", target.Provider).
		Str("model", target.ModelID).
		Msg("delegation: resolved child run target")

	spec := ChildSpec{
		SessionID:     childSessionID,
		AgentName:     row.ChildAgent,
		Profile:       profile,
		Target:        target,
		Task:          composeTask(row.Task, row.ContextText, s.spawnPreamble(ctx, row)),
		Context:       row.ContextText,
		MCPToken:      token,
		TagIDs:        effTags,
		MaxTurns:      row.MaxTurns,
		MaxTokens:     row.MaxTokens,
		WorkspacePath: workspacePath,
	}
	if err := s.Runner.StartAgent(runCtx, spec); err != nil {
		cancel()
		s.untrackInflight(id)
		unsub()
		revoke()
		s.finish(ctx, row, entity.DelegationRunning, entity.DelegationFailed, "", "spawn sub-agent: "+err.Error(), 0)
		return nil, err
	}

	cleanup := func() {
		cancel()
		s.untrackInflight(id)
		unsub()
		revoke()
		if workspacePath != "" && s.Workspaces != nil {
			if err := s.Workspaces.ReleaseWorktree(context.WithoutCancel(baseCtx), workspacePath); err != nil {
				log.Warn().Err(err).Str("path", workspacePath).Msg("delegation: worktree cleanup failed")
			}
		}
	}

	// Async: hand back a handle now and let the run finish on its own.
	// The leader is explicitly told the result is not here yet, so it
	// does not read an empty Result as "the sub-agent had nothing to say".
	if mode == ModeAsync {
		go func() {
			defer cleanup()
			res, err := s.await(baseCtx, runCtx, row, spec, ch)
			if err != nil {
				log.Error().Err(err).Str("delegation", id).Msg("delegation: async run failed")
				return
			}
			s.deliver(baseCtx, row, sink, res)
		}()
		return &Result{
			DelegationID: id,
			Profile:      row.ProfileKey,
			Status:       entity.DelegationRunning,
			Mode:         ModeBackground,
			Note: "Started in the background. The result is NOT in this reply — it will be delivered via " +
				sink + ". Say what you started and end your turn; you are woken when it lands. " +
				"Do not wait on it here, and do not invent its answer. " +
				"(wick_agent_collect with this delegation_id retrieves it manually if it never arrives.)",
			WorkspaceNote: workspaceNote,
		}, nil
	}

	defer cleanup()
	res, err := s.await(ctx, runCtx, row, spec, ch)
	if res != nil {
		res.Mode = ModeForeground
		res.WorkspaceNote = workspaceNote
	}
	return res, err
}

// deliver routes a finished async result to its sink. Best-effort by
// design: the result is already durable in the row, so a delivery
// failure is logged rather than retried into a loop.
func (s *Service) deliver(ctx context.Context, row *entity.AgentDelegation, sink string, res *Result) {
	if s.Deliver == nil || res == nil || sink == SinkNone || sink == "" {
		return
	}
	text := formatDelivery(row, res)
	var err error
	switch sink {
	case SinkChannel:
		err = s.Deliver.DeliverToChannel(ctx, row.ParentSessionID, text)
	case SinkSession:
		// Re-prompt the leader so it can act on the result. Marked
		// collected first: the callback IS the delivery, and a later
		// wick_delegate_collect must not hand the same work over twice.
		if ok, mErr := s.Repo.MarkCollected(ctx, row.ID); mErr != nil || !ok {
			return
		}
		err = s.Deliver.DeliverToSession(ctx, row.ParentSessionID, row.ParentAgent, text)
	}
	if err != nil {
		log.Error().Err(err).Str("delegation", row.ID).Str("sink", sink).
			Msg("delegation: async delivery failed; result is still recorded")
	}
}

// formatDelivery renders an async result for a human-facing sink.
func formatDelivery(row *entity.AgentDelegation, res *Result) string {
	var b strings.Builder
	// Name the agent by its handle and say how long it took. A reader
	// watching agents work needs to know WHICH one came back — and an
	// elapsed time is the difference between "it answered" and "it spent
	// twenty-eight minutes on this".
	name := row.Handle
	if name == "" {
		name = row.ProfileKey
	}
	elapsed := ""
	if row.EndedAt != nil && !row.StartedAt.IsZero() {
		elapsed = " · " + row.EndedAt.Sub(row.StartedAt).Round(time.Second).String()
	}
	b.WriteString("@" + name + " finished (" + res.Status + ")" + elapsed + "\n\n")
	// A one-line verdict before the prose. A supervisor deciding what to
	// do next reads confidence and evidence count first; making it read
	// the whole write-up to find them is what the envelope exists to stop.
	// The items themselves are NOT inlined: waking a leader with twenty
	// excerpts spends its context on data it may not need.
	if e := res.Envelope; e != nil && e.Structured {
		fmt.Fprintf(&b, "[%s confidence · %d evidence item(s) · collect %s for detail]\n\n",
			e.Confidence, len(e.Evidence), row.ID)
	}
	if res.Result != "" {
		b.WriteString(res.Result)
	} else {
		b.WriteString("(no output)")
	}
	if res.Note != "" {
		b.WriteString("\n\n" + res.Note)
	}
	return b.String()
}

// await consumes the child's event stream until it finishes, its turn
// budget runs out, or it is stopped.
func (s *Service) await(
	ctx context.Context,
	runCtx context.Context,
	row *entity.AgentDelegation,
	spec ChildSpec,
	ch <-chan StreamEvent,
) (*Result, error) {
	// Seeded from the row, not zeroed. A continued delegation (see
	// Continue) re-enters this loop on a row that already spent turns and
	// tokens, and both caps are absolute: starting the counters at zero
	// would make UpdateTurns walk the count backwards, drop the earlier
	// spend from the bill, and hand a continuation the full budget again
	// however many times it was continued.
	var (
		turns    = row.TurnsUsed
		text     strings.Builder
		usageIn  = row.InputTokens
		usageOut = row.OutputTokens
	)
	tokens := func() int { return usageIn + usageOut }

	// recordUsage persists spend as soon as it is seen.
	//
	// Deliberately called BEFORE any cancellation or cap check on the same
	// event: a turn that gets cancelled still consumed those tokens, and
	// writing usage only on the success path silently under-reports the
	// bill — the exact bug the prior art documented.
	recordUsage := func(raw string) {
		u := ParseUsage(raw)
		if u.Empty() {
			return
		}
		usageIn += u.InputTokens
		usageOut += u.OutputTokens
		_ = s.Repo.UpdateUsage(context.WithoutCancel(ctx), row.ID, usageIn, usageOut, tokens())
	}

	// finalText prefers accumulated stream text, falling back to whatever
	// the runner still holds for an in-flight turn. The fallback matters
	// on the kill paths, where the last turn never reaches Done and so
	// never lands in the accumulator.
	finalText := func() string {
		if t := strings.TrimSpace(text.String()); t != "" {
			return t
		}
		return strings.TrimSpace(s.Runner.PartialText(spec.SessionID, spec.AgentName))
	}

	for {
		if ch == nil {
			// No stream wired (tests / degraded mode): nothing to wait on.
			out := finalText()
			s.finish(ctx, row, entity.DelegationRunning, entity.DelegationDone, out, "", turns)
			return s.doneResult(ctx, row, turns, tokens(), out), nil
		}

		select {
		case <-runCtx.Done():
			// Cancelled: either a human interrupt (Interrupt cancels the
			// inflight context) or the leader's own context dying. Capture
			// partial work and hand it back as a normal result.
			out := finalText()
			_ = s.Runner.KillAgent(spec.SessionID, spec.AgentName)
			ok, _ := s.Repo.FinishGuarded(ctx, row.ID, entity.DelegationRunning, entity.DelegationInterrupted, out, "", turns)
			status := entity.DelegationInterrupted
			if !ok {
				// A completion won the race; report what actually landed.
				if fresh, err := s.Repo.Get(context.WithoutCancel(ctx), row.ID); err == nil {
					status, out, turns = fresh.Status, fresh.Result, fresh.TurnsUsed
				}
			}
			res := &Result{DelegationID: row.ID, Profile: row.ProfileKey, Status: status, TurnsUsed: turns, Result: out}
			if status == entity.DelegationInterrupted {
				res.Note = interruptNote
			}
			return res, nil

		case ev, ok := <-ch:
			if !ok {
				out := finalText()
				s.finish(ctx, row, entity.DelegationRunning, entity.DelegationDone, out, "", turns)
				return s.doneResult(ctx, row, turns, tokens(), out), nil
			}
			// Usage can ride any event, so account for it before the type
			// switch decides whether this event ends the run.
			recordUsage(ev.Raw)

			switch ev.Type {
			case event.TextDelta:
				text.WriteString(ev.Text)
			case event.Error:
				out := finalText()
				s.finish(ctx, row, entity.DelegationRunning, entity.DelegationFailed, out, ev.Text, turns)
				return &Result{
					DelegationID: row.ID, Profile: row.ProfileKey,
					Status: entity.DelegationFailed, TurnsUsed: turns, Result: out,
					Note: "Sub-agent reported an error: " + ev.Text,
				}, nil
			case event.Done:
				turns++
				_ = s.Repo.UpdateTurns(ctx, row.ID, turns)

				// Token cap: a spend ceiling bounds what turn counting
				// cannot — one turn that reads a huge file can cost more
				// than ten small ones. Only enforceable when the provider
				// actually reported usage; 0 means "unknown", not "free",
				// so an unreporting provider is bounded by turns alone.
				if spec.MaxTokens > 0 && tokens() >= spec.MaxTokens {
					out := finalText()
					_ = s.Runner.KillAgent(spec.SessionID, spec.AgentName)
					s.finish(ctx, row, entity.DelegationRunning, entity.DelegationStoppedBudget, out, "", turns)
					return &Result{
						DelegationID: row.ID, Profile: row.ProfileKey,
						Status: entity.DelegationStoppedBudget, TurnsUsed: turns,
						TokensUsed: tokens(), Result: out,
						Note: TokenBudgetNote(tokens(), spec.MaxTokens),
					}, nil
				}

				// Turn accounting is done here, on the normalized Done
				// event, rather than by trusting a provider flag. Only
				// claude exposes --max-turns; counting Done ourselves and
				// killing on overrun makes the cap behave identically on
				// providers that have no such flag, which is the whole
				// point of enforcing it wick-side.
				if turns >= spec.MaxTurns {
					out := finalText()
					_ = s.Runner.KillAgent(spec.SessionID, spec.AgentName)
					s.finish(ctx, row, entity.DelegationRunning, entity.DelegationStoppedMaxTurns, out, "", turns)
					return &Result{
						DelegationID: row.ID, Profile: row.ProfileKey,
						Status: entity.DelegationStoppedMaxTurns, TurnsUsed: turns, Result: out,
						Note: fmt.Sprintf("Stopped at max_turns=%d. The result above is partial.", spec.MaxTurns),
					}, nil
				}

				// A finished turn with STREAMED text is the sub-agent's
				// answer. One delegation is one question, so the first
				// complete turn ends the run rather than leaving the child
				// idling and holding a parallel slot.
				//
				// Deliberately reads the accumulator rather than
				// finalText(): the runner's partial buffer can hold text
				// from an unrelated in-flight turn, and treating that as
				// an answer would end the run at turn 1 and make the turn
				// cap unreachable.
				if out := strings.TrimSpace(text.String()); out != "" {
					// A peer may have asked this agent something. Its
					// closing text is the only answer that will ever
					// exist once the run ends, so promote it before
					// finishing — otherwise the asker blocks until its
					// timeout waiting on an agent that has already gone.
					if err := s.CloseUnansweredAsk(ctx, row.RootID, row.Handle, out); err != nil {
						log.Warn().Err(err).Str("handle", row.Handle).
							Msg("delegation: auto-reply on turn end failed")
					}
					// New work may have arrived while this turn ran.
					// Delivering it keeps the agent alive for another turn
					// rather than closing on the answer it just gave —
					// which is what makes a back-and-forth possible at all.
					// Bounded by the same turn cap as everything else.
					if n, derr := s.DeliverInbox(ctx, row.RootID, row.Handle); derr != nil {
						log.Warn().Err(derr).Str("handle", row.Handle).
							Msg("delegation: inbox delivery on turn end failed")
					} else if n > 0 {
						text.Reset()
						continue
					}
					return s.doneResult(ctx, row, turns, tokens(), out), nil
				}
			}
		}
	}
}

// doneResult builds the success result and records the outcome.
//
// It re-reads the row to pick up flags set by another path while the run
// was in flight — above all UserSteered, written by the take-over
// endpoint. A steered answer must be labelled as such: the leader is
// otherwise told a role produced unaided work that a human actually
// shaped.
func (s *Service) doneResult(ctx context.Context, row *entity.AgentDelegation, turns, tokens int, out string) *Result {
	out = s.authoritativeResult(ctx, row, out)
	s.finish(ctx, row, entity.DelegationRunning, entity.DelegationDone, out, "", turns)
	res := &Result{
		DelegationID: row.ID, Profile: row.ProfileKey,
		Status: entity.DelegationDone, TurnsUsed: turns,
		TokensUsed: tokens, Result: out,
	}
	// A customer-facing draft is sanitised on the way OUT, before anyone
	// can act on it. Doing it here rather than trusting the role's prompt
	// is what makes "no credentials" a control instead of a request.
	if row.ProfileKey == ClientDrafterRoleKey {
		if sanitised, masked := s.sanitizeDraft(ctx, row, out); masked {
			res.Result = sanitised
			res.Note = strings.TrimSpace(res.Note + " " + SanitizeNote)
		}
	}

	detached := context.WithoutCancel(ctx)
	if fresh, err := s.Repo.Get(detached, row.ID); err == nil {
		if fresh.UserSteered {
			res.UserSteered = true
			res.Note = "A human sent guidance to this sub-agent while it worked, so the result reflects their steering as well as the role's own reasoning."
		}
		res.Envelope = s.resolveEnvelope(detached, fresh, out)
		// Gate first, then ingest. Only well-formed evidence reaches the
		// pool, so a later contradiction is between two things somebody
		// actually quoted. Best-effort throughout: the answer is already
		// durable on the row, and failing the delegation because a
		// secondary index could not be written would be the wrong trade.
		verdict := s.GateEnvelope(detached, fresh, res.Envelope)
		if _, err := s.IngestEvidence(detached, fresh, &ResultEnvelope{Evidence: verdict.Accepted}); err != nil {
			log.Warn().Err(err).Str("delegation", row.ID).
				Msg("delegation: evidence not ingested; the result is still recorded")
		}
		s.OnDelegationFinished(detached, fresh, verdict)
	}
	return res
}

// resolveEnvelope returns the structured answer for a finished
// delegation, reconstructing one from prose when report_result was never
// called.
//
// The reconstruction is PERSISTED, not just returned, so every later
// reader — collect, the panel, the incident store — sees the same answer
// as the caller who was there when it finished.
func (s *Service) resolveEnvelope(ctx context.Context, row *entity.AgentDelegation, finalText string) *ResultEnvelope {
	if env := EnvelopeOf(row); env != nil {
		return env
	}
	env := FallbackEnvelope(finalText)
	if env.Summary == "" {
		return env
	}
	if err := s.Repo.SaveResultJSON(ctx, row.ID, env); err != nil {
		log.Warn().Err(err).Str("delegation", row.ID).
			Msg("delegation: fallback envelope not persisted")
	}
	return env
}

// finish writes a terminal status, tolerating the case where another
// path (an interrupt) already closed the row.
func (s *Service) finish(ctx context.Context, row *entity.AgentDelegation, expected, status, result, errMsg string, turns int) {
	// Detach from ctx: a cancelled request must still persist the outcome,
	// otherwise a run that ends alongside a client disconnect leaves a row
	// stuck at "running" forever.
	ok, err := s.Repo.FinishGuarded(context.WithoutCancel(ctx), row.ID, expected, status, result, errMsg, turns)
	if err != nil {
		log.Error().Err(err).Str("delegation", row.ID).Msg("delegation: finish write failed")
		return
	}
	if !ok {
		log.Debug().Str("delegation", row.ID).Str("status", status).
			Msg("delegation: finish skipped, row already terminal")
		return
	}
	// A slot just freed. Poking here rather than on a timer is what keeps
	// the gap between two queued sub-agents imperceptible.
	s.pokeSlot(row.RootID)

	// A detached run that just ended may have been the reason a channel is
	// showing "leader stopped, work still running". Re-report the survivors so
	// that notice corrects itself instead of claiming forever that a finished
	// sub-agent is still going.
	if row.Detached && row.ParentSessionID != "" {
		s.reportSurvivors(context.WithoutCancel(ctx), row.ParentSessionID)
	}
}

// reportSurvivors recomputes which detached sub-agents are still running under
// parentSessionID and hands the list to OnDetachedChange, if wired.
func (s *Service) reportSurvivors(ctx context.Context, parentSessionID string) {
	if s.OnDetachedChange == nil {
		return
	}
	rows, err := s.Repo.ListActiveDescendants(ctx, parentSessionID)
	if err != nil {
		log.Warn().Err(err).Str("session", parentSessionID).
			Msg("delegation: survivor re-report failed")
		return
	}
	var survivors []Survivor
	for i := range rows {
		if !rows[i].Detached {
			continue
		}
		survivors = append(survivors, Survivor{
			Handle:     rows[i].Handle,
			ProfileKey: rows[i].ProfileKey,
			AgentName:  rows[i].ChildAgent,
		})
	}
	s.OnDetachedChange(parentSessionID, survivors)
}

// limits resolves the governor ceilings for this call, preferring the
// live provider so an operator's change applies to the next delegation.
func (s *Service) limits() Limits {
	if s.LimitsFn != nil {
		return s.LimitsFn()
	}
	return s.Limits
}

func (s *Service) trackInflight(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight == nil {
		s.inflight = map[string]context.CancelFunc{}
	}
	s.inflight[id] = cancel
}

func (s *Service) untrackInflight(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inflight, id)
}

// cancelInflight unblocks a Run waiting on this delegation. Reports
// whether a waiter was actually there.
func (s *Service) cancelInflight(id string) bool {
	s.mu.Lock()
	cancel, ok := s.inflight[id]
	s.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
	return ok
}

// composeTask builds the child's first message. The sub-agent starts
// with a clean context — it never sees the leader's history — so any
// background it needs must be stated explicitly here.
func composeTask(task, extra, preamble string) string {
	task = strings.TrimSpace(task)
	extra = strings.TrimSpace(extra)
	preamble = strings.TrimSpace(preamble)
	if extra != "" {
		task += "\n\nContext from the delegating agent:\n" + extra
	}
	if preamble != "" {
		task += "\n\n" + preamble
	}
	return task
}

// spawnPreamble is everything wick tells a sub-agent beyond its task and
// its caller's context: what the other agents have established, and who
// they are.
//
// Best-effort throughout. A sub-agent that cannot be told these things is
// worse off, not broken, so a lookup failure costs the preamble rather
// than the spawn.
func (s *Service) spawnPreamble(ctx context.Context, row *entity.AgentDelegation) string {
	var parts []string
	if mem := s.memoryPayload(ctx, row); mem != "" {
		parts = append(parts, mem)
	}
	if roster := s.spawnRosterBlock(ctx, row); roster != "" {
		parts = append(parts, roster)
	}
	// Only for a delegation that asked to be supervised. Telling every
	// sub-agent to file progress notes would wake leaders for work nobody
	// is watching, and spend a turn on reporting for tasks that finish in
	// two.
	if row.Supervised {
		parts = append(parts, SupervisionBrief)
	}
	return strings.Join(parts, "\n")
}

// memoryPayload renders the history this delegation's memory mode calls
// for. Siblings are only fetched by the modes that use them — a
// no_history spawn must not pay for a query it will discard.
func (s *Service) memoryPayload(ctx context.Context, row *entity.AgentDelegation) string {
	mode := row.MemoryMode
	if mode == MemoryNone || mode == MemoryChunks {
		return ""
	}
	siblings, err := s.Repo.ListByRoot(ctx, row.RootID)
	if err != nil {
		log.Debug().Err(err).Str("delegation", row.ID).
			Msg("delegation: memory payload unavailable")
		return ""
	}
	trimmed := siblings[:0]
	for _, d := range siblings {
		if d.ID != row.ID {
			trimmed = append(trimmed, d)
		}
	}
	// Only looked up for the modes that render it: a tree with no
	// incident is the common case, and the query would be discarded.
	inc, err := s.IncidentForRoot(ctx, row.RootID)
	if err != nil {
		log.Debug().Err(err).Str("delegation", row.ID).
			Msg("delegation: incident state unavailable for the memory payload")
	}
	return MemoryPayload(mode, trimmed, inc)
}

// spawnRosterBlock builds the colleague list a sub-agent starts with.
//
// Best-effort: a sub-agent that cannot be told who else exists is worse
// off, but not broken, so a lookup failure costs the block rather than
// the spawn.
func (s *Service) spawnRosterBlock(ctx context.Context, row *entity.AgentDelegation) string {
	roster, budget, err := s.rosterAndBudget(ctx, row.RootID)
	if err != nil {
		log.Debug().Err(err).Str("delegation", row.ID).
			Msg("delegation: spawn roster unavailable")
		return ""
	}
	// The spawning agent's own row is already in the roster; naming itself
	// as a colleague invites it to message itself.
	trimmed := roster[:0]
	for _, e := range roster {
		if e.Handle != row.Handle {
			trimmed = append(trimmed, e)
		}
	}
	var spawnable []string
	if res, rerr := s.NewResolver(ctx, row.RootID, row.ProjectID); rerr == nil {
		spawnable = res.SpawnableRoles()
	}
	return FormatRosterBlock(trimmed, spawnable, budget)
}

// ErrNoService is returned by wiring when delegation is not configured.
var ErrNoService = errors.New("delegation service not configured")
