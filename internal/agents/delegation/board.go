package delegation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// Task board + Kanban (Phases 7 & 8).
//
// The board is a queue of work that outlives any single conversation:
// tasks are enqueued by a human or an agent, claimed by workers, executed
// as delegations, and completed with evidence. Kanban is the same data
// viewed as columns.
//
// Three rules carried over from the prior art, each fixing a bug seen in
// a shipped system:
//
//  1. CLAIM WITH A GUARDED UPDATE. Two workers polling at once must not
//     both win the same task, and the guard belongs in the WHERE clause,
//     not in an application-level check a future caller could forget.
//  2. STAGE ≠ COLUMN ID. Columns are presentation; the state machine
//     reads Stage. Otherwise renaming a column breaks automation.
//  3. ONE POLICY EVALUATOR FOR EVERY SURFACE. Agents call MCP directly,
//     so a gate enforced only on the HTTP path would simply be bypassed
//     by the agent it was meant to constrain.

var (
	// ErrBoardNotFound is returned when a board key does not resolve.
	ErrBoardNotFound = errors.New("board not found")
	// ErrTaskNotFound is returned when a task id does not resolve.
	ErrTaskNotFound = errors.New("task not found")
	// ErrTaskClaimed means another worker already holds the task.
	ErrTaskClaimed = errors.New("task already claimed by another worker")
	// ErrEvidenceRequired means a blocking gate refused a completion that
	// carried no proof.
	ErrEvidenceRequired = errors.New("this board requires evidence before a task can be completed")
)

// BoardColumn is one column in the Kanban view.
type BoardColumn struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Stage is the state-machine value this column represents. Several
	// columns may map to one stage (e.g. two review lanes).
	Stage string `json:"stage"`
	Order int    `json:"order"`
}

// DefaultColumns is the starting layout for a new board.
func DefaultColumns() []BoardColumn {
	return []BoardColumn{
		{ID: "backlog", Title: "Backlog", Stage: entity.StageBacklog, Order: 0},
		{ID: "ready", Title: "Ready", Stage: entity.StageReady, Order: 1},
		{ID: "running", Title: "In progress", Stage: entity.StageRunning, Order: 2},
		{ID: "review", Title: "Review", Stage: entity.StageReview, Order: 3},
		{ID: "done", Title: "Done", Stage: entity.StageDone, Order: 4},
	}
}

// DecodeColumns reads a board's column layout, falling back to the
// default set when the column blob is missing or unreadable — a board
// with no renderable columns would be unusable.
func DecodeColumns(raw string) []BoardColumn {
	if raw == "" {
		return DefaultColumns()
	}
	var out []BoardColumn
	if err := json.Unmarshal([]byte(raw), &out); err != nil || len(out) == 0 {
		return DefaultColumns()
	}
	return out
}

// EncodeColumns serialises a column layout.
func EncodeColumns(cols []BoardColumn) string {
	b, err := json.Marshal(cols)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// StageForColumn resolves a column id to its stage. Unknown columns fall
// back to backlog rather than to a terminal stage: a mis-mapped column
// must never look like completed work.
func StageForColumn(cols []BoardColumn, columnID string) string {
	for _, c := range cols {
		if c.ID == columnID {
			return c.Stage
		}
	}
	return entity.StageBacklog
}

// ---------- board CRUD ----------

// ListBoards returns boards, optionally including disabled ones.
func (r *Repo) ListBoards(ctx context.Context, includeDisabled bool) ([]entity.AgentBoard, error) {
	q := r.db.WithContext(ctx).Order("key asc")
	if !includeDisabled {
		q = q.Where("disabled = ?", false)
	}
	var out []entity.AgentBoard
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetBoard resolves one board by key.
func (r *Repo) GetBoard(ctx context.Context, key string) (*entity.AgentBoard, error) {
	var b entity.AgentBoard
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&b).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBoardNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// SaveBoard inserts or updates a board.
func (r *Repo) SaveBoard(ctx context.Context, b *entity.AgentBoard) error {
	return r.db.WithContext(ctx).Save(b).Error
}

// DeleteBoard removes a board and its tasks. Tasks are removed too:
// leaving them orphaned would keep them claimable by workers polling a
// board that no longer exists.
func (r *Repo) DeleteBoard(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("board_id = ?", id).Delete(&entity.AgentTask{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&entity.AgentBoard{}).Error
	})
}

// ---------- tasks ----------

// ListTasks returns a board's tasks in display order.
func (r *Repo) ListTasks(ctx context.Context, boardID string) ([]entity.AgentTask, error) {
	var out []entity.AgentTask
	err := r.db.WithContext(ctx).
		Where("board_id = ?", boardID).
		Order("stage asc, position asc, created_at asc").
		Find(&out).Error
	return out, err
}

// GetTask loads one task.
func (r *Repo) GetTask(ctx context.Context, id string) (*entity.AgentTask, error) {
	var t entity.AgentTask
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTask enqueues work.
func (r *Repo) CreateTask(ctx context.Context, t *entity.AgentTask) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.Stage == "" {
		t.Stage = entity.StageBacklog
	}
	if t.ClaimState == "" {
		t.ClaimState = entity.ClaimUnclaimed
	}
	return r.db.WithContext(ctx).Create(t).Error
}

// SaveTask updates a task wholesale (edit from the board UI).
func (r *Repo) SaveTask(ctx context.Context, t *entity.AgentTask) error {
	return r.db.WithContext(ctx).Save(t).Error
}

// DeleteTask removes a task.
func (r *Repo) DeleteTask(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.AgentTask{}).Error
}

// ClaimTask atomically takes the next ready task for a worker.
//
// The claim is a guarded UPDATE: the row must still be unclaimed and in
// the ready stage for the write to land. Two workers racing therefore
// produce exactly one winner by construction — no application-level
// "check then set" that a concurrent caller could interleave with.
//
// Returns (nil, nil) when there is nothing to claim.
func (r *Repo) ClaimTask(ctx context.Context, boardID, workerID, profileKey string) (*entity.AgentTask, error) {
	for attempt := 0; attempt < 5; attempt++ {
		q := r.db.WithContext(ctx).
			Where("board_id = ? AND stage = ? AND claim_state = ?",
				boardID, entity.StageReady, entity.ClaimUnclaimed)
		if profileKey != "" {
			// A worker only claims work meant for its own role, or work
			// that named no role at all.
			q = q.Where("profile_key = ? OR profile_key = ?", profileKey, "")
		}
		var candidate entity.AgentTask
		err := q.Order("priority desc, created_at asc").First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}

		now := time.Now().UTC()
		res := r.db.WithContext(ctx).Model(&entity.AgentTask{}).
			Where("id = ? AND claim_state = ?", candidate.ID, entity.ClaimUnclaimed).
			Updates(map[string]any{
				"claim_state": entity.ClaimClaimed,
				"claimed_by":  workerID,
				"claimed_at":  &now,
			})
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			// Another worker won this one; try the next candidate.
			continue
		}
		candidate.ClaimState = entity.ClaimClaimed
		candidate.ClaimedBy = workerID
		candidate.ClaimedAt = &now
		return &candidate, nil
	}
	return nil, nil
}

// StartTask marks a claimed task as executing and links its delegation.
// Guarded on the claim so a worker cannot start a task it does not hold.
func (r *Repo) StartTask(ctx context.Context, taskID, workerID, delegationID string) error {
	res := r.db.WithContext(ctx).Model(&entity.AgentTask{}).
		Where("id = ? AND claim_state = ? AND claimed_by = ?", taskID, entity.ClaimClaimed, workerID).
		Updates(map[string]any{
			"claim_state":   entity.ClaimStarted,
			"stage":         entity.StageRunning,
			"delegation_id": delegationID,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTaskClaimed
	}
	return nil
}

// CompleteTask finishes a task, applying the board's evidence gate.
//
// The gate is evaluated HERE — one evaluator, shared by the HTTP handlers
// and the MCP tools. A check that lived only in the web layer would be
// bypassed by every agent, which is precisely the caller it is meant to
// constrain.
func (r *Repo) CompleteTask(ctx context.Context, board *entity.AgentBoard, taskID, workerID, result, evidence string, failed bool) error {
	if !failed && board != nil && board.GateMode == entity.GateModeBlocking && evidence == "" {
		return ErrEvidenceRequired
	}

	now := time.Now().UTC()
	stage := entity.StageDone
	claim := entity.ClaimComplete
	if failed {
		stage = entity.StageBlocked
		claim = entity.ClaimFailed
	} else if board != nil && board.GateMode != entity.GateModeOff {
		// A gated board parks finished work in review rather than calling
		// it done on the agent's own say-so.
		stage = entity.StageReview
	}

	updates := map[string]any{
		"claim_state": claim,
		"stage":       stage,
		"result":      result,
		"evidence":    evidence,
		"ended_at":    &now,
	}
	if failed {
		updates["error_msg"] = result
	}

	res := r.db.WithContext(ctx).Model(&entity.AgentTask{}).
		Where("id = ? AND claim_state IN ? AND claimed_by = ?",
			taskID, []string{entity.ClaimClaimed, entity.ClaimStarted}, workerID).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrTaskClaimed
	}
	return nil
}

// MoveTask moves a card to another column (the Kanban drag).
//
// Writes BOTH the column id and the stage it maps to, so the board can be
// re-titled or re-ordered freely without the state machine noticing.
func (r *Repo) MoveTask(ctx context.Context, board *entity.AgentBoard, taskID, columnID string, position int) error {
	if board == nil {
		return ErrBoardNotFound
	}
	cols := DecodeColumns(board.Columns)
	stage := StageForColumn(cols, columnID)

	// Moving INTO a terminal stage is a completion, so the same evidence
	// gate applies as the API path — otherwise a drag would launder work
	// past a rule the tool call has to obey.
	if stage == entity.StageDone && board.GateMode == entity.GateModeBlocking {
		t, err := r.GetTask(ctx, taskID)
		if err != nil {
			return err
		}
		if t.Evidence == "" {
			return ErrEvidenceRequired
		}
	}
	return r.db.WithContext(ctx).Model(&entity.AgentTask{}).
		Where("id = ?", taskID).
		Updates(map[string]any{
			"column_id": columnID,
			"stage":     stage,
			"position":  position,
		}).Error
}

// ReleaseStaleClaims returns tasks whose worker went away back to the
// queue.
//
// Without this a crashed worker's claim pins a task forever: it is not
// unclaimed, so nobody else may take it, and it is not running, so
// nothing will ever finish it.
func (r *Repo) ReleaseStaleClaims(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	res := r.db.WithContext(ctx).Model(&entity.AgentTask{}).
		Where("claim_state = ? AND claimed_at IS NOT NULL AND claimed_at < ?",
			entity.ClaimClaimed, cutoff).
		Updates(map[string]any{
			"claim_state": entity.ClaimUnclaimed,
			"claimed_by":  "",
			"claimed_at":  nil,
		})
	return res.RowsAffected, res.Error
}

// GateWarning returns the advisory shown when a non-blocking board
// receives a completion with no evidence. Empty when nothing to warn about.
func GateWarning(board *entity.AgentBoard, evidence string) string {
	if board == nil || evidence != "" || board.GateMode != entity.GateModeWarning {
		return ""
	}
	return fmt.Sprintf("Board %q expects evidence of completion; none was supplied.", board.Key)
}
