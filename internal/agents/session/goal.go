package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// goal.go is the durable open/done latch written by the shared `todo`
// tool when goal mode is engaged. The wick engine force-continues a turn
// while status=open; other providers still get durable state for resume
// (their agentic loop is outside wick, so they don't force-continue).
//
// File: <SessionDir>/goal.json. One surface with todo — no separate goal tool.

const goalFileName = "goal.json"

// GoalStatus is the lifecycle of one session goal.
type GoalStatus string

const (
	GoalOpen      GoalStatus = "open"
	GoalDone      GoalStatus = "done"
	GoalAbandoned GoalStatus = "abandoned"
)

// Goal is the on-disk record.
type Goal struct {
	Goal      string     `json:"goal"`
	Status    GoalStatus `json:"status"`
	Note      string     `json:"note,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func goalPath(layout agentconfig.Layout, id string) string {
	return filepath.Join(layout.SessionDir(id), goalFileName)
}

// LoadGoalDir reads goal.json from an absolute session directory
// (engine path — it already holds SessionDir, not layout+id).
func LoadGoalDir(sessionDir string) (*Goal, error) {
	if sessionDir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(sessionDir, goalFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var g Goal
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// HasOpenGoalDir is the engine-side form of HasOpenGoal.
func HasOpenGoalDir(sessionDir string) bool {
	g, err := LoadGoalDir(sessionDir)
	return err == nil && g != nil && g.Status == GoalOpen
}

// LoadGoal reads the session's goal latch. Missing file → (nil, nil).
func LoadGoal(layout agentconfig.Layout, id string) (*Goal, error) {
	if id == "" {
		return nil, nil
	}
	return LoadGoalDir(layout.SessionDir(id))
}

func saveGoal(layout agentconfig.Layout, id string, g *Goal) error {
	if id == "" {
		return fmt.Errorf("goal: empty session id")
	}
	g.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	dir := layout.SessionDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(goalPath(layout, id), b, 0o644)
}

// HasOpenGoal reports whether the session has an open goal latch.
func HasOpenGoal(layout agentconfig.Layout, id string) bool {
	g, err := LoadGoal(layout, id)
	return err == nil && g != nil && g.Status == GoalOpen
}

// OpenGoal writes/replaces an open latch.
func OpenGoal(layout agentconfig.Layout, id, goal, note string) error {
	if id == "" || goal == "" {
		return fmt.Errorf("goal: session id and goal text required")
	}
	return saveGoal(layout, id, &Goal{Goal: goal, Status: GoalOpen, Note: note})
}

// CompleteGoal marks the open latch done. No open goal → no-op.
func CompleteGoal(layout agentconfig.Layout, id, note string) error {
	g, err := LoadGoal(layout, id)
	if err != nil {
		return err
	}
	if g == nil || g.Status != GoalOpen {
		return nil
	}
	g.Status = GoalDone
	g.Note = note
	return saveGoal(layout, id, g)
}

// AbandonGoal marks the open latch abandoned. No open goal → no-op.
func AbandonGoal(layout agentconfig.Layout, id, note string) error {
	g, err := LoadGoal(layout, id)
	if err != nil {
		return err
	}
	if g == nil || g.Status != GoalOpen {
		return nil
	}
	g.Status = GoalAbandoned
	g.Note = note
	return saveGoal(layout, id, g)
}
