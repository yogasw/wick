package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/config"
)

// inflightLockPath is the per-session turn lock file next to inflight.jsonl.
func inflightLockPath(layout config.Layout, sessionID string) string {
	return filepath.Join(layout.SessionDir(sessionID), "inflight.lock")
}

// InflightGuard is a held turn lock taken by someone other than the turn's
// own Store — boot recovery — so that no Store starts streaming into
// inflight.jsonl while the leftover is being folded. Release it when done.
type InflightGuard struct{ l *inflightLock }

// Release drops the lock. Safe on nil.
func (g *InflightGuard) Release() {
	if g != nil && g.l != nil {
		g.l.release()
	}
}

// TryLockInflight takes the session's turn lock without blocking. ok=false
// means a live turn — in this process or a draining predecessor — owns the
// session's inflight.jsonl right now, so it must not be recovered or have
// its status reset.
//
// A session that never ran a turn under this build has no lock file, so
// nobody can hold one: that is free, and no lock file is created for it —
// boot walks every session and must not litter each folder.
func TryLockInflight(layout config.Layout, sessionID string) (*InflightGuard, bool) {
	path := inflightLockPath(layout, sessionID)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return &InflightGuard{}, true
	}
	l, ok := tryLockInflight(path)
	if !ok {
		return nil, false
	}
	return &InflightGuard{l: l}, true
}
