package entity

import "time"

// Task-board + Kanban storage.
//
// Two lessons from the prior art are baked into these columns:
//
//  1. STAGE IS SEPARATE FROM COLUMN ID. A user may rename or reorder
//     columns freely; the state machine keys on Stage. Without that split,
//     `if columnID == "dev"` ends up scattered through the code and
//     renaming a column silently breaks automation.
//  2. EVIDENCE-GATED COMPLETION. A board can require proof before a task
//     may enter a terminal stage, instead of trusting an agent's own
//     claim that it finished.

// Board stages. Columns map onto these; the set itself is fixed.
const (
	StageBacklog = "backlog"
	StageReady   = "ready"
	StageRunning = "running"
	StageReview  = "review"
	StageDone    = "done"
	StageBlocked = "blocked"
)

// BoardStages is the canonical ordered stage list.
var BoardStages = []string{
	StageBacklog, StageReady, StageRunning, StageReview, StageDone, StageBlocked,
}

// Gate modes for evidence-gated completion. "warning" exists so a board
// can be switched on without immediately blocking everyone — soft launch
// first, enforce once the evidence habit is established.
const (
	GateModeOff      = "off"
	GateModeWarning  = "warning"
	GateModeBlocking = "blocking"
)

// AgentBoard is one task board.
type AgentBoard struct {
	ID          string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	Key         string `gorm:"type:varchar(128);uniqueIndex;not null" json:"key"`
	Name        string `gorm:"type:varchar(256);not null;default:''" json:"name"`
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	// Columns is a JSON array of {id,title,stage,order}. Presentation
	// only — the state machine reads Stage, never the column id.
	Columns string `gorm:"type:text;not null;default:'[]'" json:"columns"`
	// SquadKey scopes which agents claim from this board.
	SquadKey string `gorm:"type:varchar(128);not null;default:''" json:"squad_key"`
	// GateMode controls evidence-gated completion: off | warning | blocking.
	GateMode string `gorm:"type:varchar(16);not null;default:'off'" json:"gate_mode"`
	// AutoDelegate makes entering the `ready` stage enqueue work
	// automatically — the column-as-automation-trigger model.
	AutoDelegate bool   `gorm:"not null;default:false" json:"auto_delegate"`
	Disabled     bool   `gorm:"not null;default:false" json:"disabled"`
	CreatedBy    string `gorm:"type:varchar(128);not null;default:''" json:"created_by"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (AgentBoard) TableName() string { return "agent_boards" }

// Task claim states, distinct from Stage: Stage is where a human sees the
// card, ClaimState is whether a worker holds it.
const (
	ClaimUnclaimed = "unclaimed"
	ClaimClaimed   = "claimed"
	ClaimStarted   = "started"
	ClaimComplete  = "complete"
	ClaimFailed    = "failed"
)

// AgentTask is one unit of work on a board.
//
// Claiming is what makes concurrent workers safe: a worker takes a task
// with a guarded update (unclaimed → claimed for exactly this worker), so
// two agents polling simultaneously cannot both win the same task. The
// guard lives in the SQL WHERE clause, not in an application-level check
// that a future caller could forget.
type AgentTask struct {
	ID      string `gorm:"primaryKey;type:varchar(64)" json:"id"`
	BoardID string `gorm:"type:varchar(64);not null;index" json:"board_id"`
	Title   string `gorm:"type:varchar(512);not null;default:''" json:"title"`
	Body    string `gorm:"type:text;not null;default:''" json:"body"`
	// Stage drives the state machine; ColumnID is only where it renders.
	Stage    string `gorm:"type:varchar(32);not null;index" json:"stage"`
	ColumnID string `gorm:"type:varchar(64);not null;default:''" json:"column_id"`
	Position int    `gorm:"not null;default:0" json:"position"`
	// ProfileKey is the role that should execute this task.
	ProfileKey string `gorm:"type:varchar(128);not null;default:''" json:"profile_key"`
	Priority   int    `gorm:"not null;default:0" json:"priority"`

	ClaimState string     `gorm:"type:varchar(32);not null;default:'unclaimed';index" json:"claim_state"`
	ClaimedBy  string     `gorm:"type:varchar(128);not null;default:''" json:"claimed_by"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	// DelegationID links a started task to the delegation executing it.
	DelegationID string `gorm:"type:varchar(64);not null;default:''" json:"delegation_id"`

	Result string `gorm:"type:text;not null;default:''" json:"result"`
	// Evidence is the proof of completion a gated board demands (test
	// output, a report, a link). Checked against GateMode on the move
	// into a terminal stage.
	Evidence string `gorm:"type:text;not null;default:''" json:"evidence"`
	ErrorMsg string `gorm:"type:text;not null;default:''" json:"error_msg"`

	CreatedBy string `gorm:"type:varchar(128);not null;default:''" json:"created_by"`
	CreatedAt time.Time
	UpdatedAt time.Time
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

func (AgentTask) TableName() string { return "agent_tasks" }
