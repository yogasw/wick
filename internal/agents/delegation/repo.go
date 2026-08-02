// Package delegation implements synchronous sub-agent delegation: a
// leader agent calls the wick_delegate MCP tool, wick spawns an isolated
// child session for the requested profile, waits for it to finish, and
// hands the final text back as a tool result.
//
// Design: internal/planning/todo/multi-agent/design.md
//
// Three invariants hold everywhere in this package:
//
//  1. Access attaches to the HUMAN who triggered the root delegation.
//     A profile can only narrow that set, never widen it.
//  2. Every stopping condition is enforced wick-side (turn counting via
//     event.Done + pool kill), so it works identically on providers that
//     have no native --max-turns flag.
//  3. Stopping a sub-agent returns PARTIAL WORK to the leader as a normal
//     tool result, never an error — an error tends to make the model
//     retry immediately after a human asked it to stop.
package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

var (
	// ErrProfileNotFound is returned when a profile key does not resolve.
	ErrProfileNotFound = errors.New("agent profile not found")
	// ErrDelegationNotFound is returned when a delegation id does not resolve.
	ErrDelegationNotFound = errors.New("delegation not found")
	// ErrForbidden means the actor may not act on this delegation.
	ErrForbidden = errors.New("forbidden")
)

// Repo is the gorm-backed store for profiles and delegations. Methods
// take a context so requests cancel cleanly.
type Repo struct {
	db *gorm.DB
}

// NewRepo wraps a live gorm handle.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// DB exposes the underlying handle for callers that need to compose a
// query (monitor aggregates). Nil-safe callers should check Repo != nil.
func (r *Repo) DB() *gorm.DB { return r.db }

// ---------- profiles ----------

// ListProfiles returns every profile, optionally including disabled ones.
func (r *Repo) ListProfiles(ctx context.Context, includeDisabled bool) ([]entity.AgentProfile, error) {
	q := r.db.WithContext(ctx).Order("key asc")
	if !includeDisabled {
		q = q.Where("disabled = ?", false)
	}
	var out []entity.AgentProfile
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetProfile resolves one profile by its stable key.
func (r *Repo) GetProfile(ctx context.Context, key string) (*entity.AgentProfile, error) {
	var p entity.AgentProfile
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListProfilesInScopes returns the global roles plus the ones owned by
// projectID, unmerged. Shadowing is applied by ResolveScoped, not here —
// the repo issues the query, the pure function holds the rule.
//
// The scope predicate is an IN rather than "a = ? OR b = ?" because an OR
// combined with the disabled filter needs explicit grouping to mean what
// it looks like it means.
func (r *Repo) ListProfilesInScopes(ctx context.Context, projectID string, includeDisabled bool) ([]entity.AgentProfile, error) {
	q := r.db.WithContext(ctx).
		Where("project_id IN (?)", []string{"", projectID}).
		Order("key asc")
	if !includeDisabled {
		q = q.Where("disabled = ?", false)
	}
	var out []entity.AgentProfile
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetProfileScoped resolves the role a session in projectID gets for key,
// with the project's version shadowing a global one of the same name.
// This is the runtime lookup — what a delegation actually spawns.
func (r *Repo) GetProfileScoped(ctx context.Context, projectID, key string) (*entity.AgentProfile, error) {
	rows, err := r.ListProfilesInScopes(ctx, projectID, true)
	if err != nil {
		return nil, err
	}
	for _, p := range ResolveScoped(rows, projectID) {
		if p.Key == key {
			found := p
			return &found, nil
		}
	}
	return nil, ErrProfileNotFound
}

// GetProfileExact looks up a row in one scope only, with no global
// fallback. Save-dedup MUST use this: resolving instead would make
// "create a project role named like a global one" find the global row and
// overwrite it, silently editing the role every other project sees.
func (r *Repo) GetProfileExact(ctx context.Context, projectID, key string) (*entity.AgentProfile, error) {
	var p entity.AgentProfile
	err := r.db.WithContext(ctx).
		Where("project_id = ? AND key = ?", projectID, key).
		First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetProfileByID resolves one profile by primary key.
func (r *Repo) GetProfileByID(ctx context.Context, id string) (*entity.AgentProfile, error) {
	var p entity.AgentProfile
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// SaveProfile inserts or updates a profile row.
func (r *Repo) SaveProfile(ctx context.Context, p *entity.AgentProfile) error {
	return r.db.WithContext(ctx).Save(p).Error
}

// DeleteProfile removes a profile by id.
func (r *Repo) DeleteProfile(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.AgentProfile{}).Error
}

// ---------- delegations ----------

// Create inserts a new delegation row.
func (r *Repo) Create(ctx context.Context, d *entity.AgentDelegation) error {
	return r.db.WithContext(ctx).Create(d).Error
}

// Get loads one delegation by id.
func (r *Repo) Get(ctx context.Context, id string) (*entity.AgentDelegation, error) {
	var d entity.AgentDelegation
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDelegationNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// FindByChildSession resolves the delegation that OWNS a session, i.e.
// the row whose child_session_id is sessionID.
//
// This is how a nested call locates its place in the tree: when a
// sub-agent itself calls wick_delegate, its own session is some other
// delegation's child, and that parent row carries the root id, depth and
// ancestor chain the governor needs. Returns (nil, nil) for a normal
// top-level session, which simply has no owning delegation.
func (r *Repo) FindByChildSession(ctx context.Context, sessionID string) (*entity.AgentDelegation, error) {
	var d entity.AgentDelegation
	err := r.db.WithContext(ctx).Where("child_session_id = ?", sessionID).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListByParent returns delegations whose direct parent is parentSessionID,
// newest first. Drives the rail panel for one conversation.
func (r *Repo) ListByParent(ctx context.Context, parentSessionID string) ([]entity.AgentDelegation, error) {
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("parent_session_id = ?", parentSessionID).
		Order("started_at asc").
		Find(&out).Error
	return out, err
}

// ListByRoot returns every delegation in one tree, at any depth.
func (r *Repo) ListByRoot(ctx context.Context, rootID string) ([]entity.AgentDelegation, error) {
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("root_id = ?", rootID).
		Order("depth asc, started_at asc").
		Find(&out).Error
	return out, err
}

// ListActiveByRoot returns the non-terminal delegations in a tree.
func (r *Repo) ListActiveByRoot(ctx context.Context, rootID string) ([]entity.AgentDelegation, error) {
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("root_id = ? AND status IN ?", rootID,
			[]string{entity.DelegationQueued, entity.DelegationRunning}).
		Order("depth desc, started_at asc").
		Find(&out).Error
	return out, err
}

// ListActiveDescendants returns every non-terminal delegation reachable
// from sessionID, at any depth, deepest first.
//
// Deepest-first ordering matters for cascade teardown: killing a
// grandchild before its parent means the parent cannot observe a child
// exit and immediately re-delegate during the teardown window.
func (r *Repo) ListActiveDescendants(ctx context.Context, sessionID string) ([]entity.AgentDelegation, error) {
	live := []string{entity.DelegationQueued, entity.DelegationRunning}
	seen := map[string]bool{sessionID: true}
	frontier := []string{sessionID}
	var out []entity.AgentDelegation

	// Walk the parent→child edges breadth-first. The tree is small
	// (max_depth defaults to 3, max_parallel to 4) so a handful of
	// indexed queries beats a recursive CTE that only some drivers share.
	for len(frontier) > 0 {
		var rows []entity.AgentDelegation
		err := r.db.WithContext(ctx).
			Where("parent_session_id IN ? AND status IN ?", frontier, live).
			Find(&rows).Error
		if err != nil {
			return nil, err
		}
		var next []string
		for _, d := range rows {
			out = append(out, d)
			if d.ChildSessionID != "" && !seen[d.ChildSessionID] {
				seen[d.ChildSessionID] = true
				next = append(next, d.ChildSessionID)
			}
		}
		frontier = next
	}
	// Deepest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// SaveDelegationForTest overwrites a delegation row wholesale. Test-only
// seam: production code mutates rows through the guarded helpers so the
// status transitions stay race-safe.
func (r *Repo) SaveDelegationForTest(ctx context.Context, d *entity.AgentDelegation) error {
	return r.db.WithContext(ctx).Save(d).Error
}

// ListRecent returns the most recent delegations across every tree, for
// the fleet monitor. Bounded by limit so a long-lived install does not
// load its whole history into one page.
func (r *Repo) ListRecent(ctx context.Context, limit int) ([]entity.AgentDelegation, error) {
	if limit <= 0 {
		limit = 200
	}
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Order("started_at desc").
		Limit(limit).
		Find(&out).Error
	return out, err
}

// ListDetached returns async delegations that must survive their leader.
func (r *Repo) ListDetached(ctx context.Context, parentSessionID string) ([]entity.AgentDelegation, error) {
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("parent_session_id = ? AND detached = ?", parentSessionID, true).
		Order("started_at asc").
		Find(&out).Error
	return out, err
}

// SumTokensByRoot returns aggregate token spend across a whole tree.
func (r *Repo) SumTokensByRoot(ctx context.Context, rootID string) (int, error) {
	var sum *int
	err := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ?", rootID).
		Select("SUM(tokens_used)").Scan(&sum).Error
	if err != nil || sum == nil {
		return 0, err
	}
	return *sum, nil
}

// UpdateUsage records token spend mid-run.
//
// Called BEFORE any cancellation check on the turn that produced it: a
// cancelled turn still cost money, and skipping the write there
// under-reports spend in a way nobody notices until the bill arrives.
func (r *Repo) UpdateUsage(ctx context.Context, id string, in, out, total int) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"input_tokens":  in,
			"output_tokens": out,
			"tokens_used":   total,
		}).Error
}

// MarkCollected flags an async result as delivered to its leader, so a
// second wick_delegate_collect does not hand back the same work twice.
// Guarded on the current flag so concurrent collects cannot both win.
func (r *Repo) MarkCollected(ctx context.Context, id string) (bool, error) {
	res := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ? AND collected = ?", id, false).
		Updates(map[string]any{"collected": true})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// MarkSteered flags a delegation a human sent a message into.
func (r *Repo) MarkSteered(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ?", id).
		Updates(map[string]any{"user_steered": true}).Error
}

// ListCollectable returns finished async delegations a leader has not
// picked up yet.
func (r *Repo) ListCollectable(ctx context.Context, parentSessionID string) ([]entity.AgentDelegation, error) {
	var out []entity.AgentDelegation
	err := r.db.WithContext(ctx).
		Where("parent_session_id = ? AND mode = ? AND collected = ?", parentSessionID, ModeAsync, false).
		Order("started_at asc").
		Find(&out).Error
	return out, err
}

// CountActiveByRoot counts in-flight delegations in a tree — the
// max_parallel check.
func (r *Repo) CountActiveByRoot(ctx context.Context, rootID string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ? AND status IN ?", rootID,
			[]string{entity.DelegationQueued, entity.DelegationRunning}).
		Count(&n).Error
	return n, err
}

// SumTurnsByRoot returns the aggregate turns consumed across a whole
// tree — the per-root budget check.
func (r *Repo) SumTurnsByRoot(ctx context.Context, rootID string) (int, error) {
	var sum *int
	err := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("root_id = ?", rootID).
		Select("SUM(turns_used)").Scan(&sum).Error
	if err != nil || sum == nil {
		return 0, err
	}
	return *sum, nil
}

// MarkRunning flips a queued delegation to running.
func (r *Repo) MarkRunning(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ? AND status = ?", id, entity.DelegationQueued).
		Updates(map[string]any{"status": entity.DelegationRunning}).Error
}

// UpdateTurns records progress mid-run so the monitor and the per-root
// budget see live numbers rather than only end-of-run totals.
func (r *Repo) UpdateTurns(ctx context.Context, id string, turns int) error {
	return r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ?", id).
		Updates(map[string]any{"turns_used": turns}).Error
}

// FinishGuarded writes a terminal status ONLY IF the row is still in
// expectedStatus, and reports whether it won.
//
// This is the idempotency guard, and it deliberately lives in the
// storage layer rather than in the handlers. A concurrent completion and
// a human interrupt race constantly; guarding on the expected prior
// status makes the loser a no-op BY CONSTRUCTION, instead of relying on
// every call site remembering to re-check. The HTTP layer still returns
// 409, but correctness does not depend on it doing so.
func (r *Repo) FinishGuarded(
	ctx context.Context,
	id, expectedStatus, newStatus, result, errMsg string,
	turns int,
) (bool, error) {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&entity.AgentDelegation{}).
		Where("id = ? AND status = ?", id, expectedStatus).
		Updates(map[string]any{
			"status":     newStatus,
			"result":     result,
			"error_msg":  errMsg,
			"turns_used": turns,
			"ended_at":   &now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// decodeStringSlice reads a JSON array column, tolerating empty/invalid
// values as "no entries" — a malformed ACL column must never be read as
// a wider permission set.
func decodeStringSlice(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// encodeStringSlice serialises a JSON array column.
func encodeStringSlice(in []string) string {
	if len(in) == 0 {
		return "[]"
	}
	b, err := json.Marshal(in)
	if err != nil {
		return "[]"
	}
	return string(b)
}
