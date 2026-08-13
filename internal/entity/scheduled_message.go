package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ScheduledMessage is one future message injection into an agent session.
// The agent can schedule itself ("check back at 12:40") or a user/scheduler
// can queue a nudge into a session that has gone idle. When run_at passes,
// the schedule runner delivers Message into the target session through the
// normal pool send path (role=user, source="schedule") — so a fired
// schedule behaves exactly like a regular inbound message: it spawns the
// session if idle, or queues behind an in-flight turn if busy.
//
// The target is not necessarily a pre-existing session. SessionMode decides
// how it is resolved at each fire: "existing" nudges SessionID (the
// original behavior), while "new" and "template" are PROJECT-scoped and
// materialize a session per fire — fresh context every run, or a
// deterministic id shared by fires in the same day/month. See
// schedule.ResolveTarget.
//
// Provenance is first-class: OwnerUserID records WHO the schedule belongs
// to (copied from the target session's — or project's — owner at create
// time) and drives both access control and the dashboard's per-owner view.
// SourceSessionID records the session the request originated in, so "who
// asked" survives even when a schedule targets a different session than
// the one that created it.
type ScheduledMessage struct {
	ID string `gorm:"type:varchar(40);primaryKey"`
	// SessionID is the session the message is delivered into for
	// SessionMode "existing". Empty on project-scoped schedules, where the
	// target is resolved per fire.
	SessionID string `gorm:"type:varchar(128);index"`
	// ProjectID is the project a project-scoped fire runs in. Required for
	// SessionMode "new" / "template" — it decides the spawned session's
	// cwd. Empty for "existing", where the project comes from the target
	// session's own meta at fire time.
	ProjectID string `gorm:"type:varchar(64);index"`
	// SessionMode is how the delivery target is resolved at fire time:
	// "existing" (default) | "new" | "template".
	SessionMode string `gorm:"type:varchar(16);not null;default:'existing'"`
	// SessionTemplate is the session-id pattern for SessionMode
	// "template", e.g. "daily-report-{date}". Placeholders are rendered
	// against the fire time; see schedule.RenderTemplate.
	SessionTemplate string `gorm:"type:varchar(128)"`
	// LastSessionID is the session the most recent fire actually landed
	// in. Read-only provenance for the UI (links the row to its run).
	LastSessionID string `gorm:"type:varchar(128)"`
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
	// RunCount is how many times this schedule has fired on its own
	// cadence. A manual run (RunNow) does NOT increment it, so max_runs
	// still means "this many scheduled fires".
	RunCount int `gorm:"default:0"`
	// LastRunAt is when it last fired (nil until the first fire), manual
	// runs included — it answers "when did this last do something".
	LastRunAt *time.Time
	// ManualRuns counts fires triggered by RunNow. Kept separate from
	// RunCount so a manual test can't consume a capped schedule's budget,
	// while still being visible.
	ManualRuns int `gorm:"default:0"`
	// Anchor is the origin of the cadence series for an interval-based
	// recurring schedule: fire N is Anchor + N*interval. Holding the origin
	// (rather than advancing from whenever a fire happened to land) is what
	// keeps the series ABSOLUTE — a pause/resume or a manual run cannot
	// drift it. Zero on cron schedules, which recompute from the expression,
	// and on one-shots.
	Anchor *time.Time
	// ManualFire marks the in-flight claim as a manual run, so the runner
	// knows not to advance the cadence or count it. Cleared on finalize.
	ManualFire bool `gorm:"default:false"`
	// PendingRunAt holds the real next fire while a manual run borrows
	// RunAt to become due. Finalize restores it, so "run it now" never
	// costs the schedule its place in the series.
	PendingRunAt *time.Time

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
	// ScheduledStatusPaused is a REPORTED status only, never stored: a paused
	// schedule keeps status=active (it holds its place in the series) with
	// paused=true beside it. Derived by EffectiveStatus so callers have a
	// status value to read and to filter on.
	ScheduledStatusPaused = "paused"
)

// ScheduledCreatedBy values.
const (
	ScheduledByAI   = "ai"
	ScheduledByUser = "user"
	ScheduledByAPI  = "api"
)

// Session target modes. "existing" is the original behavior and the default
// for legacy rows (the column defaults to it, and an empty value is treated
// as "existing" everywhere).
const (
	// ScheduledSessionExisting delivers into the fixed SessionID.
	ScheduledSessionExisting = "existing"
	// ScheduledSessionNew mints a fresh session in ProjectID on every fire
	// — clean context each run.
	ScheduledSessionNew = "new"
	// ScheduledSessionTemplate renders SessionTemplate against the fire
	// time and reuses that session when it already exists.
	ScheduledSessionTemplate = "template"
)

// IsRecurring reports whether this schedule repeats.
func (s *ScheduledMessage) IsRecurring() bool { return s.Kind == ScheduledKindRecurring }

// IsLive reports whether the schedule can still fire.
func (s *ScheduledMessage) IsLive() bool {
	return s.Status == ScheduledStatusPending || s.Status == ScheduledStatusActive
}

// EffectiveStatus is the status to REPORT, which folds the paused flag in.
//
// Paused is stored as a flag beside status (a paused schedule is still
// "active" — it keeps its place in the series and resumes into it), but a
// caller reading only `status` then sees "active" for something that will not
// fire, and has no status value to filter on. So the wire form derives
// "paused"; the stored status stays untouched.
func (s *ScheduledMessage) EffectiveStatus() string {
	if s.Paused && s.IsLive() {
		return ScheduledStatusPaused
	}
	return s.Status
}

// claimParkYears mirrors the far-future offset ClaimDue writes into run_at
// while a fire is in flight (see Store.ClaimDue). Terminal transitions unpark
// the row, so a stored sentinel means "claimed but not yet finalized".
const claimParkYears = 100

// NextRunAt is the fire time safe to publish. It returns nil when there is no
// future fire to report — a terminal row, or one parked mid-claim.
//
// The park sentinel (~100 years out) exists only to keep a crashed claim from
// re-firing; it is an internal marker, never a real schedule. Handing it to a
// client made a finished schedule read as "next run in 2126" and sort ahead of
// everything in a run_at ordering, so it is filtered here rather than trusted
// to have been cleaned up by every transition.
func (s *ScheduledMessage) NextRunAt() *time.Time {
	if !s.IsLive() {
		return nil
	}
	// A manual run borrows RunAt to become due, parking the real next fire in
	// PendingRunAt. Reporting the borrowed value said "next run: now", which
	// reads as though run_now had moved the schedule — the opposite of what it
	// does. Always answer with the schedule's OWN next fire.
	if s.ManualFire && s.PendingRunAt != nil {
		t := *s.PendingRunAt
		return &t
	}
	if s.RunAt.IsZero() {
		return nil
	}
	if s.RunAt.After(time.Now().AddDate(claimParkYears-1, 0, 0)) {
		return nil // parked mid-claim; no meaningful next fire yet
	}
	t := s.RunAt
	return &t
}

// ManualFirePending reports whether a manual run is queued or in flight. The
// API surfaces it separately from the schedule's own timing so a caller can
// tell "an extra fire is coming" from "the schedule moved".
func (s *ScheduledMessage) ManualFirePending() bool { return s.ManualFire }

// Mode returns the effective session mode, normalizing the empty value that
// legacy rows carry to "existing".
func (s *ScheduledMessage) Mode() string {
	if s.SessionMode == "" {
		return ScheduledSessionExisting
	}
	return s.SessionMode
}

// IsProjectScoped reports whether the target session is resolved per fire
// (and thus materialized in ProjectID) rather than fixed at create time.
func (s *ScheduledMessage) IsProjectScoped() bool {
	return s.Mode() != ScheduledSessionExisting
}

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
	if s.SessionMode == "" {
		s.SessionMode = ScheduledSessionExisting
	}
	// Seed the cadence anchor from the first fire, so an interval schedule's
	// series is fixed from the start (fire N = anchor + N*interval) and no
	// later action can drift it. Cron rows don't need one — the expression is
	// the series.
	if s.Anchor == nil && s.Kind == ScheduledKindRecurring && s.IntervalMs > 0 && !s.RunAt.IsZero() {
		anchor := s.RunAt
		s.Anchor = &anchor
	}
	// A negative MaxRuns would slip past the advance() stop check
	// (MaxRuns > 0 && …), turning a "capped" recurring schedule into an
	// unbounded one. Clamp to 0 = no cap, the intended meaning.
	if s.MaxRuns < 0 {
		s.MaxRuns = 0
	}
	if s.Status == "" {
		s.Status = s.LiveStatus()
	}
	return nil
}
