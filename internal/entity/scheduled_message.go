package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ScheduledMessage is one future message injection into an agent session.
// The agent can schedule itself ("check back at 12:40") or a user/scheduler
// can queue a nudge into a session that has gone idle. When run_at passes,
// the schedule runner delivers Message into SessionID through the normal
// pool send path (role=user, source="schedule") — so a fired schedule
// behaves exactly like a regular inbound message: it spawns the session if
// idle, or queues behind an in-flight turn if busy.
//
// Provenance is first-class: OwnerUserID records WHO the schedule belongs
// to (copied from the target session's owner at create time) and drives
// both access control and the dashboard's per-owner view. SourceSessionID
// records the session the request originated in, so "who asked" survives
// even when a schedule targets a different session than the one that
// created it.
type ScheduledMessage struct {
	ID string `gorm:"type:varchar(40);primaryKey"`
	// SessionID is the session the message is delivered into.
	SessionID string `gorm:"type:varchar(128);index;not null"`
	// OwnerUserID is who the schedule belongs to — copied from the target
	// session's Meta.UserID at create time (empty for legacy/unowned
	// sessions). Access control and the dashboard scope on this.
	OwnerUserID string `gorm:"type:varchar(36);index"`
	// CreatedBy records how the schedule was made: "ai" (agent scheduled
	// itself), "user" (dashboard), or "api" (external caller).
	CreatedBy string `gorm:"type:varchar(16)"`
	// SourceSessionID is the session the schedule was requested from. Usually
	// equals SessionID, but kept explicit so provenance is preserved when a
	// schedule targets a different session.
	SourceSessionID string `gorm:"type:varchar(128)"`
	// AgentName is the pool agent to route the delivered message to; default
	// "main".
	AgentName string `gorm:"type:varchar(64);not null;default:'main'"`
	// Message is the prompt injected as a role=user turn when the schedule
	// fires.
	Message string `gorm:"type:text;not null"`

	// Kind is "once" (fire a single time) or "recurring" (fire repeatedly on
	// Interval/Cron until cancelled or a stop condition is met).
	Kind string `gorm:"type:varchar(16);not null;default:'once'"`
	// RunAt is the NEXT concrete fire time (UTC) the runner claims on. For a
	// one-shot it is the single fire time; for a recurring schedule it is
	// advanced after each fire. Named RunAt (not NextRunAt) so the runner's
	// claim query — run_at <= now — is uniform across both kinds.
	RunAt time.Time `gorm:"index;not null"`
	// IntervalMs, when > 0 on a recurring schedule, is the fixed gap between
	// fires in milliseconds ("every 5m"). Mutually exclusive with Cron.
	IntervalMs int64 `gorm:"default:0"`
	// Cron, when set on a recurring schedule, is a 5-field cron expression
	// (min hour dom mon dow) picking fire minutes ("0 9 * * 1"). Mutually
	// exclusive with IntervalMs.
	Cron string `gorm:"type:varchar(128)"`

	// Paused, when true on a recurring schedule, suspends firing without
	// deleting the row. Resume clears it and recomputes RunAt.
	Paused bool `gorm:"default:false"`
	// MaxRuns > 0 caps the number of fires for a recurring schedule; after
	// the RunCount reaches it the schedule finishes (status=done). 0 = no cap.
	MaxRuns int `gorm:"default:0"`
	// EndsAt, when non-nil, stops a recurring schedule once RunAt passes it.
	EndsAt *time.Time
	// RunCount is how many times this schedule has fired.
	RunCount int `gorm:"default:0"`
	// LastRunAt is when it last fired (nil until the first fire).
	LastRunAt *time.Time

	// Status: pending | active | done | cancelled | failed.
	//   once:      pending → done   (or failed / cancelled)
	//   recurring: active  → active … → done  (max_runs/ends_at) / cancelled / failed
	Status string `gorm:"type:varchar(16);index;not null;default:'pending'"`
	// Attempts counts delivery attempts on the CURRENT fire (reset each fire).
	Attempts int `gorm:"default:0"`
	// LastError holds the most recent delivery failure reason.
	LastError string `gorm:"type:text"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Schedule kind.
const (
	ScheduledKindOnce      = "once"
	ScheduledKindRecurring = "recurring"
)

// Schedule status constants. Kept as strings so the wire/DB form stays
// stable across builds.
const (
	ScheduledStatusPending   = "pending" // one-shot, not yet fired
	ScheduledStatusActive    = "active"   // recurring, live
	ScheduledStatusDone      = "done"     // finished (one-shot fired, or recurring hit its stop condition)
	ScheduledStatusCancelled = "cancelled"
	ScheduledStatusFailed    = "failed"
)

// ScheduledCreatedBy values.
const (
	ScheduledByAI   = "ai"
	ScheduledByUser = "user"
	ScheduledByAPI  = "api"
)

// IsRecurring reports whether this schedule repeats.
func (s *ScheduledMessage) IsRecurring() bool { return s.Kind == ScheduledKindRecurring }

// LiveStatus is the status a fresh row of this kind starts in: recurring
// schedules are "active", one-shots are "pending".
func (s *ScheduledMessage) LiveStatus() string {
	if s.IsRecurring() {
		return ScheduledStatusActive
	}
	return ScheduledStatusPending
}

func (s *ScheduledMessage) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = "sm_" + uuid.NewString()
	}
	if s.AgentName == "" {
		s.AgentName = "main"
	}
	if s.Kind == "" {
		s.Kind = ScheduledKindOnce
	}
	if s.Status == "" {
		s.Status = s.LiveStatus()
	}
	return nil
}
