package schedule

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// WatchCreate is everything a new watch is checked against before it is
// stored, whichever surface creates it (the MCP tool or a session panel).
// One function so the two cannot drift: the same steps rules, interval
// floor, per-user cap and Bash gate apply to both.
type WatchCreate struct {
	Steps []Step
	// Spec is the parsed timing; a watch must recur at ≥ MinWatchInterval.
	Spec Spec
	// Timeout is the raw time limit (see WatchTimeout).
	Timeout string
	// OnMatch is stop (default) or continue.
	OnMatch string
	// OwnerUserID is who the watch runs as — its creator (see WatchOwner).
	OwnerUserID string
	// CreatorUserID is the caller; its admin role is what lets bash steps
	// pass an agent held to gate rules. "" = no known caller, never admin.
	CreatorUserID string
	// CreatorIsAdmin skips the live-watch cap, as on every other path.
	CreatorIsAdmin bool
	// SessionIDs are the agents whose Bash switch the steps must pass: the
	// session asking and the target.
	SessionIDs []string
	ProjectID  string
}

// WatchOwner is the owner of a new row. A watch runs code and connector
// calls as its owner, so it belongs to whoever creates it — never to the
// owner of the session or project it reports into (scopeOwner). Without a
// known creator (stdio, internal) it falls back to the scope's owner. A
// message schedule always keeps the scope's owner.
func WatchOwner(isWatch bool, creatorUserID, scopeOwner string) string {
	if isWatch && creatorUserID != "" {
		return creatorUserID
	}
	return scopeOwner
}

// PrepareWatch validates a new watch and returns its encoded steps and its
// ends_at (nil = no time limit: a run_at watch, "off", or a cron watch
// without one). The cap is fail-closed: a count that cannot be read refuses.
func (s *Store) PrepareWatch(ctx context.Context, in WatchCreate, now time.Time) (string, *time.Time, error) {
	if err := ValidateSteps(in.Steps); err != nil {
		return "", nil, err
	}
	if err := ValidateWatchTiming(in.Spec.Recurring, in.Spec.IntervalMs); err != nil {
		return "", nil, err
	}
	onMatch, err := NormalizeOnMatch(in.OnMatch)
	if err != nil {
		return "", nil, err
	}
	d, err := WatchTimeout(in.Timeout, in.Spec.Cron != "")
	if err != nil {
		return "", nil, err
	}
	if !in.CreatorIsAdmin {
		if err := s.CheckWatchCap(ctx, in.OwnerUserID); err != nil {
			return "", nil, err
		}
	}
	if err := CheckBashAllowed(ctx, in.Steps, in.SessionIDs, in.ProjectID, in.CreatorUserID); err != nil {
		return "", nil, err
	}
	enc, err := EncodeWatch(in.Steps, onMatch)
	if err != nil {
		return "", nil, err
	}
	if d == 0 || !in.Spec.Recurring {
		return enc, nil, nil
	}
	until := now.Add(d)
	return enc, &until, nil
}

// DryRunWatch is the "test before saving" of a watch that does not exist
// yet: the steps run once as ownerUserID against sessionID, through the
// same path as TestWatch (one test at a time, identity + Bash re-checked).
// creatorUserID is the caller, as in WatchCreate. The transient row has no
// id, so nothing is written to any history.
func DryRunWatch(ctx context.Context, steps []Step, sessionID, ownerUserID, creatorUserID string) (*RunRecord, error) {
	if err := ValidateSteps(steps); err != nil {
		return nil, err
	}
	if err := CheckBashAllowed(ctx, steps, []string{sessionID}, "", creatorUserID); err != nil {
		return nil, err
	}
	m := entity.ScheduledMessage{
		Type:            entity.ScheduledTypeWatch,
		SessionID:       sessionID,
		SourceSessionID: sessionID,
		OwnerUserID:     ownerUserID,
		Message:         "dry run",
	}
	return TestWatch(ctx, m, steps)
}
