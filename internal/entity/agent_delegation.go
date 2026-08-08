package entity

import "time"

// Delegation status values. Strings (not iota) so rows stay
// self-describing for ad-hoc queries.
//
// The four terminal-by-failure states are deliberately distinct because
// they mean different things to both the leader and the operator:
//
//   - DelegationInterrupted    — a HUMAN stopped it.
//   - DelegationStoppedMaxTurns — the per-sub-agent turn cap hit.
//   - DelegationStoppedBudget   — the per-root aggregate budget ran out.
//   - DelegationFailed          — a runtime error.
//
// Collapsing them loses the ability to tell "the user changed their mind"
// from "the agent ran away", which is exactly the distinction the leader
// needs in order to decide whether retrying is sensible.
//
// SubAgentStatuses below is the single source of truth; the TypeScript
// union is generated against it by a test so the two lists cannot drift.
const (
	DelegationQueued          = "queued"
	DelegationRunning         = "running"
	DelegationDone            = "done"
	DelegationFailed          = "failed"
	DelegationInterrupted     = "interrupted"
	DelegationStoppedMaxTurns = "stopped_max_turns"
	DelegationStoppedBudget   = "stopped_budget"
)

// DelegationStatuses is the complete, ordered status set. Kept in one
// place so the enum-drift test and any UI mapping read the same list.
var DelegationStatuses = []string{
	DelegationQueued,
	DelegationRunning,
	DelegationDone,
	DelegationFailed,
	DelegationInterrupted,
	DelegationStoppedMaxTurns,
	DelegationStoppedBudget,
}

// TerminalDelegationStatuses is every status in which no further work is
// happening. Exported as a slice because the guard on reopening a
// delegation is a SQL `status IN (...)` and cannot call a predicate;
// keeping one list means the query and IsTerminalDelegationStatus can
// never disagree about what "finished" means.
var TerminalDelegationStatuses = []string{
	DelegationDone,
	DelegationFailed,
	DelegationInterrupted,
	DelegationStoppedMaxTurns,
	DelegationStoppedBudget,
}

// IsTerminalDelegationStatus reports whether no further work will happen
// on a delegation in this status. Used by Interrupt to short-circuit
// before killing anything.
func IsTerminalDelegationStatus(s string) bool {
	for _, t := range TerminalDelegationStatuses {
		if s == t {
			return true
		}
	}
	return false
}

// AgentDelegation is the audit + control record for exactly one
// wick_delegate call. It is written before the child spawns and updated
// as the child progresses, so an interrupted or crashed run still leaves
// a durable row behind.
//
// RootID + Depth are what keep recursion bounded: the governor counts
// aggregate turns per RootID and refuses to spawn past MaxDepth. A tree
// without those two columns has no enforceable stopping condition.
type AgentDelegation struct {
	ID string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	// RootID is the id of the top-most delegation in this tree; equal to
	// ID when Depth == 0. Turn budget is pooled across the whole root.
	RootID string `gorm:"type:varchar(64);not null;index" json:"root_id"`
	// ParentSessionID is the leader session that called wick_delegate.
	ParentSessionID string `gorm:"type:varchar(128);not null;index" json:"parent_session_id"`
	ParentAgent     string `gorm:"type:varchar(128);not null;default:''" json:"parent_agent"`
	ProfileKey      string `gorm:"type:varchar(128);not null" json:"profile_key"`
	// ChildSessionID is the isolated execution context spawned for this
	// delegation. A real session (own transcript + store), just hidden
	// from the conversation list.
	ChildSessionID string `gorm:"type:varchar(128);not null;index" json:"child_session_id"`
	ChildAgent     string `gorm:"type:varchar(128);not null;default:''" json:"child_agent"`

	Task  string `gorm:"type:text;not null;default:''" json:"task"`
	Depth int    `gorm:"not null;default:0" json:"depth"`
	Mode  string `gorm:"type:varchar(16);not null;default:'sync'" json:"mode"`
	// AncestorKeys is the JSON-encoded profile-key chain from root to
	// parent. The cycle guard rejects a delegation whose profile already
	// appears here, which is what stops A→B→A from looping forever.
	AncestorKeys string `gorm:"type:text;not null;default:'[]'" json:"ancestor_keys"`

	// DeliverySink routes an async result: "channel" posts back to the
	// originating thread, "session" re-prompts the leader (collect),
	// "none" leaves it for the monitor only.
	DeliverySink string `gorm:"type:varchar(32);not null;default:''" json:"delivery_sink"`
	// Detached marks an async delegation that must outlive its leader.
	// A detached sub-agent is NOT torn down when the leader goes idle or
	// is killed — it was fired deliberately to run on its own.
	Detached bool `gorm:"not null;default:false" json:"detached"`
	// Collected marks an async result the leader has already picked up
	// via wick_delegate_collect, so a second call does not re-deliver it.
	Collected bool `gorm:"not null;default:false" json:"collected"`

	// ProjectID is the scope the delegation ran in, copied from the parent
	// session. Roles are scoped, so resolving ProfileKey later without it
	// would find the GLOBAL role of the same name — a different prompt and
	// a different tool list, with nothing to signal the mismatch.
	ProjectID string `gorm:"type:varchar(64);not null;default:'';index" json:"project_id"`

	Workspace     string `gorm:"type:varchar(16);not null;default:'shared'" json:"workspace"`
	WorkspacePath string `gorm:"type:text;not null;default:''" json:"workspace_path"`
	// WorkspaceNote records why a requested workspace mode was not honoured
	// (e.g. worktree asked for on a non-git project, fell back to shared).
	// Surfaced to the leader so a silent downgrade never looks like success.
	WorkspaceNote string `gorm:"type:text;not null;default:''" json:"workspace_note"`
	// SquadKey links this delegation to the named squad that produced it.
	SquadKey string `gorm:"type:varchar(128);not null;default:'';index" json:"squad_key"`
	// UserSteered marks a delegation a human sent a message into
	// mid-flight. The result is no longer purely the sub-agent's own work,
	// and the leader is told so rather than being handed a steered answer
	// as though the role produced it unaided.
	UserSteered bool `gorm:"not null;default:false" json:"user_steered"`

	Status    string `gorm:"type:varchar(32);not null;index" json:"status"`
	TurnsUsed int    `gorm:"not null;default:0" json:"turns_used"`
	MaxTurns  int    `gorm:"not null;default:0" json:"max_turns"`

	// Token accounting. Populated when the provider reports usage in its
	// stream; 0 means "this provider did not tell us", never "free".
	//
	// Usage is recorded BEFORE any cancellation check, deliberately: a
	// turn that gets cancelled still consumed tokens, and skipping the
	// write there silently under-reports spend.
	InputTokens  int `gorm:"not null;default:0" json:"input_tokens"`
	OutputTokens int `gorm:"not null;default:0" json:"output_tokens"`
	TokensUsed   int `gorm:"not null;default:0" json:"tokens_used"`
	// MaxTokens is the per-delegation token cap, 0 = uncapped by the
	// delegation itself (the per-tree budget still applies).
	MaxTokens int `gorm:"not null;default:0" json:"max_tokens"`
	// Result carries the final answer on success and the PARTIAL text on
	// interrupt / turn-exhaustion. Never empty-on-stop by design: the
	// leader receives partial work as a normal tool result rather than an
	// error, because an error tends to trigger a blind retry right after
	// the user asked for a stop.
	Result   string `gorm:"type:text;not null;default:''" json:"result"`
	ErrorMsg string `gorm:"type:text;not null;default:''" json:"error_msg"`
	// ResultJSON is the structured envelope a sub-agent reported through
	// report_result, or one reconstructed from Result when it never
	// called. Result stays authoritative for humans; this column exists
	// so a supervisor can act on an answer without re-reading prose.
	ResultJSON string `gorm:"type:text;not null;default:''" json:"result_json,omitempty"`

	// TriggeredBy is the human user id at the root of this tree. Drives
	// both tag inheritance and interrupt authorization; it is NOT the
	// leader agent's identity.
	TriggeredBy string     `gorm:"type:varchar(128);not null;default:'';index" json:"triggered_by"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`

	// LastReport is the sub-agent's own most recent progress note, filed
	// through the progress op while it was still working.
	//
	// Only the LATEST is kept, deliberately. This is a "where are you now"
	// field read by whoever is supervising, not a log: a growing history
	// would be read in full on every check, and the sub-agent's earlier
	// positions are already in its transcript.
	LastReport   string     `gorm:"type:text;not null;default:''" json:"last_report,omitempty"`
	LastReportAt *time.Time `json:"last_report_at,omitempty"`
	// Supervised records that this delegation was asked to file progress
	// notes. Read at spawn to decide whether the preamble tells it to; a
	// short task should not pay for reporting nobody asked for.
	Supervised bool `gorm:"not null;default:false" json:"supervised,omitempty"`

	// Handle is this instance's address inside its tree ("reviewer-2").
	// Unique per RootID and never reused, because a handle that could be
	// recycled would let a message land on a different agent than the one
	// the sender was talking to.
	Handle string `gorm:"type:varchar(64);not null;default:''" json:"handle"`
	// HopCount is the number of consecutive agent-to-agent messages in
	// this tree since a human last spoke. Stored on the ROOT row only, and
	// reset only by a human turn — a leader that could reset its own limit
	// would not be limited.
	HopCount int `gorm:"not null;default:0" json:"hop_count"`

	// Blocked marks a RUNNING delegation whose agent is waiting on a
	// synchronous child rather than working. A blocked row does not hold a
	// parallel slot — without this, a serial room deadlocks the moment a
	// sub-agent delegates: the parent owns the only slot while its child
	// queues behind it, and neither can ever proceed.
	Blocked bool `gorm:"not null;default:false" json:"blocked"`
	// ContextText preserves the caller's `context` argument so a delegation
	// that waits in the queue can be started later exactly as it was
	// requested. Without it a queued row would lose the background its
	// caller supplied, and the sub-agent would start on a thinner task than
	// the one that was asked for.
	ContextText string `gorm:"type:text;not null;default:''" json:"-"`
	// MemoryMode is the resolved choice of what this sub-agent was told
	// about the rest of the conversation. Stored so a delegation that
	// waits in the queue is started with the memory its caller asked for,
	// not with whatever the default happens to be by then.
	MemoryMode string `gorm:"type:varchar(32);not null;default:''" json:"memory_mode,omitempty"`
	// IncidentID links this delegation to its tree's incident record, once
	// one exists. This column is the whole of what a separate agent_tasks
	// table would have been: status, timestamps, error and once-only
	// collection already live on this row, under test.
	IncidentID string `gorm:"type:varchar(64);not null;default:'';index" json:"incident_id,omitempty"`
	// Iteration is the checker round this delegation belongs to, stamped
	// from the incident when it was created. Round completion is a query
	// over it rather than a judgement.
	Iteration int `gorm:"not null;default:0" json:"iteration"`
	// IntakeReasked records that the mechanical gate already sent this
	// sub-agent one follow-up about malformed evidence. Bounded at one:
	// unbounded re-asking turns a turn budget into an argument about
	// formatting.
	IntakeReasked bool `gorm:"not null;default:false" json:"intake_reasked,omitempty"`
	// CollectNudged records that the leader has already been woken once
	// about this uncollected async result. Without it a sweep every
	// fifteen seconds becomes a wake every fifteen seconds.
	CollectNudged bool `gorm:"not null;default:false" json:"collect_nudged,omitempty"`
}

// LeaderHandle is the address of the conversation owner (the MAIN agent).
// Reserved: a profile literally named "main" must not be able to take the
// leader's address and inherit the authority that goes with it.
const LeaderHandle = "main"

func (AgentDelegation) TableName() string { return "agent_delegations" }
