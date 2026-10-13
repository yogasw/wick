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
	"errors"
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
	// retired: the pool dropped this buffer from its map (empty, its slot
	// released). A caller that looked it up just before still holds it; a
	// message it adds then goes to the session's live buffer through
	// revive instead of an object nobody drains any more.
	retired bool
	revive  func(*Buffer) *Buffer
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
	if live := b.live(); live != b {
		return live.Append(text)
	}
	b.mu.Lock()
	b.lines = append(b.lines, text)
	snapshot := append([]string(nil), b.lines...)
	b.mu.Unlock()
	return b.persist(snapshot)
}

// Prepend puts texts, in order, ahead of everything buffered: a failover
// re-send is the turn the held messages came after. Nil-safe like Drain.
func (b *Buffer) Prepend(texts ...string) error {
	if b == nil {
		return errors.New("no buffer")
	}
	if live := b.live(); live != b {
		return live.Prepend(texts...)
	}
	b.mu.Lock()
	b.lines = append(append([]string(nil), texts...), b.lines...)
	snapshot := append([]string(nil), b.lines...)
	b.mu.Unlock()
	return b.persist(snapshot)
}

// Drain returns all buffered lines joined by newline and clears the
// buffer (both in-memory and on-disk PendingInput). Returns "" if
// empty.
//
// Nil-safe: a nil buffer drains to "". Drain is reached from bare
// goroutines (queue grants), where a nil deref is not a recovered
// request error but the whole process going down — it did, once, when a
// wake-path spawn arrived without a buffer. spawn now guarantees the
// buffer exists; this guard is the backstop for the next wiring gap.
func (b *Buffer) Drain() (string, error) {
	if b == nil {
		return "", nil
	}
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

// retireIfEmpty marks an empty buffer retired; false (kept) when it
// holds messages. Caller holds the pool's mu.
func (b *Buffer) retireIfEmpty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) > 0 {
		return false
	}
	b.retired = true
	return true
}

// live is the buffer a write goes to: b, unless the pool retired it.
func (b *Buffer) live() *Buffer {
	b.mu.Lock()
	retired, revive := b.retired, b.revive
	b.mu.Unlock()
	if !retired || revive == nil {
		return b
	}
	return revive(b)
}

// Lines returns a copy of what is buffered, without draining it.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.lines...)
}

// DropHead removes text when it is the first buffered line — a failover
// re-send its spawn never delivered. false = the head is something else
// (already drained), nothing changed.
func (b *Buffer) DropHead(text string) (bool, error) {
	b.mu.Lock()
	if len(b.lines) == 0 || b.lines[0] != text {
		b.mu.Unlock()
		return false, nil
	}
	b.lines = append([]string(nil), b.lines[1:]...)
	snapshot := append([]string(nil), b.lines...)
	b.mu.Unlock()
	return true, b.persist(snapshot)
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
