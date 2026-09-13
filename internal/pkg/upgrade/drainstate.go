package upgrade

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/yogasw/wick/internal/processctl"
)

// drainstate.go lets a DRAINING process say what it is still finishing, to a
// process that is not it.
//
// A draining parent has already handed over the socket, so it answers no
// requests: everything it knows about its own remaining work — the agent turn
// it is waiting on, the workflow run mid-node — is invisible from the
// outside. Meanwhile the successor cannot start a third generation while that
// parent lives, so the UI could only say "previous process still finishing,
// retrying in 15s". True, and useless: it names who is blocking without ever
// saying why, which is the one thing an operator needs to decide between
// waiting and forcing.
//
// So the parent leaves its outstanding list beside the pid file, refreshed
// while it drains and removed when it exits.
const drainStateFile = "draining.json"

// DrainState is what a draining process publishes about itself.
type DrainState struct {
	PID         int       `json:"pid"`
	Since       time.Time `json:"since"`
	Outstanding []string  `json:"outstanding"`
	// UpdatedAt lets a reader ignore a record left behind by a process that
	// died without cleaning up.
	UpdatedAt time.Time `json:"updated_at"`
}

func drainStatePath(dir string) string { return filepath.Join(dir, drainStateFile) }

// PublishDrainState writes what this process is still waiting for.
func PublishDrainState(dir string, pid int, since time.Time, outstanding []string) {
	PublishDrainStateAt(dir, pid, since, outstanding, time.Now())
}

// PublishDrainStateAt is PublishDrainState with an explicit timestamp, so a
// test can write a record that is already stale.
func PublishDrainStateAt(dir string, pid int, since time.Time, outstanding []string, at time.Time) {
	if dir == "" {
		return
	}
	b, err := json.Marshal(DrainState{PID: pid, Since: since, Outstanding: outstanding, UpdatedAt: at})
	if err != nil {
		return
	}
	//nolint:errcheck // best-effort: a missing file only costs the UI its detail
	_ = os.WriteFile(drainStatePath(dir), b, 0o644)
}

// ClearDrainState removes the record. Called as the process exits, so a
// successor does not report a drain that finished.
func ClearDrainState(dir string) {
	if dir == "" {
		return
	}
	//nolint:errcheck
	_ = os.Remove(drainStatePath(dir))
}

// ReadDrainState returns the record left by a still-living predecessor.
//
// Two things make a record stale: the process is gone, or nothing has
// refreshed it recently. Both mean the drain is over and the file is litter —
// reporting it would leave the UI blaming a process that no longer exists.
func ReadDrainState(dir string) (DrainState, bool) {
	if dir == "" {
		return DrainState{}, false
	}
	b, err := os.ReadFile(drainStatePath(dir))
	if err != nil {
		return DrainState{}, false
	}
	var st DrainState
	if json.Unmarshal(b, &st) != nil || st.PID <= 0 {
		return DrainState{}, false
	}
	if time.Since(st.UpdatedAt) > 30*time.Second {
		return DrainState{}, false
	}
	if !processctl.ProcessAlive(st.PID) {
		return DrainState{}, false
	}
	return st, true
}
