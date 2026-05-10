// Package pool manages the global agent subprocess slot count + FIFO
// queue. Files:
//
//   - buffer.go — per-session message buffer (drain on slot grant)
//   - pool.go   — slot allocation, FIFO queue, factory hookup
//
// Buffer rationale: when a session is queued (no slot), incoming user
// messages must not vanish — they're appended to the on-disk
// PendingInput list (so a wick restart preserves them) AND held in a
// transient buffer here. When the slot is granted, the entire buffer
// is drained as one combined input to the spawned agent. See
// agents-design.md §5.1.1.
package pool

import (
	"sync"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// Buffer is a small per-session message queue. Operations are
// idempotent w.r.t. the on-disk meta.json so a crash between in-memory
// append and disk persist doesn't lose user input.
type Buffer struct {
	layout    config.Layout
	sessionID string

	mu    sync.Mutex
	lines []string
}

// NewBuffer reads any pending_input persisted on disk so we resume
// queued messages after a wick restart.
func NewBuffer(layout config.Layout, sessionID string) (*Buffer, error) {
	b := &Buffer{layout: layout, sessionID: sessionID}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return nil, err
	}
	b.lines = append(b.lines, sess.Meta.PendingInput...)
	return b, nil
}

// Append adds a new line and persists it to meta.PendingInput so a
// crash before drain doesn't drop it.
func (b *Buffer) Append(text string) error {
	b.mu.Lock()
	b.lines = append(b.lines, text)
	snapshot := append([]string(nil), b.lines...)
	b.mu.Unlock()
	return b.persist(snapshot)
}

// Drain returns all buffered lines joined by newline and clears the
// buffer (both in-memory and on-disk PendingInput). Returns "" if
// empty.
func (b *Buffer) Drain() (string, error) {
	b.mu.Lock()
	if len(b.lines) == 0 {
		b.mu.Unlock()
		return "", nil
	}
	combined := joinLines(b.lines)
	b.lines = nil
	b.mu.Unlock()
	if err := b.persist(nil); err != nil {
		return combined, err
	}
	return combined, nil
}

// Len reports the buffered line count without modifying state.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// persist rewrites meta.PendingInput. Caller passes the snapshot to
// avoid holding b.mu during disk I/O.
func (b *Buffer) persist(snapshot []string) error {
	sess, err := session.Load(b.layout, b.sessionID)
	if err != nil {
		return err
	}
	sess.Meta.PendingInput = snapshot
	return session.SaveMeta(b.layout, b.sessionID, sess.Meta)
}

// joinLines concatenates with `\n`. Doesn't add a trailing newline —
// the agent's Send wrapper handles framing.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	if len(lines) == 1 {
		return lines[0]
	}
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	out := make([]byte, 0, total)
	for i, l := range lines {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, l...)
	}
	return string(out)
}
