package airouter

import (
	"bytes"
	"sync"
)

// logBuffer is a thread-safe, bounded sink for a router process's
// stdout+stderr. It satisfies io.Writer (passed as cmd.Stdout/Stderr) and
// keeps only the most recent maxBytes so a long-running process can't grow
// it without bound. Snapshot returns the current contents for a fresh
// subscriber's initial paint; every Write also fans the new chunk out to
// live SSE subscribers so the log panel tails in real time.
type logBuffer struct {
	mu     sync.Mutex
	buf    []byte
	subs   map[int]chan string
	nextID int
}

// maxLogBytes caps the retained log tail (~64 KiB is plenty for a glance).
const maxLogBytes = 64 * 1024

// logSubBuffer bounds how many chunks a slow log subscriber may queue.
const logSubBuffer = 64

func newLogBuffer() *logBuffer { return &logBuffer{subs: make(map[int]chan string)} }

// Write appends p, trimming from the front to stay under maxLogBytes, and
// fans the new chunk out to any live subscribers. Trimming snaps to the
// next newline so we never show a half line.
func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > maxLogBytes {
		over := len(l.buf) - maxLogBytes
		if nl := bytes.IndexByte(l.buf[over:], '\n'); nl >= 0 {
			over += nl + 1
		}
		l.buf = append(l.buf[:0], l.buf[over:]...)
	}
	chunk := string(p)
	for _, ch := range l.subs {
		select {
		case ch <- chunk:
		default: // slow subscriber — drop this chunk for it
		}
	}
	l.mu.Unlock()
	return len(p), nil
}

// Snapshot returns a copy of the retained log tail.
func (l *logBuffer) Snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

// Reset clears the buffer — called on each (re)start so the panel shows
// only the current process's output. Live subscribers are notified with a
// reset sentinel so they clear their view too.
func (l *logBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = l.buf[:0]
	for _, ch := range l.subs {
		select {
		case ch <- logResetSentinel:
		default:
		}
	}
}

// logResetSentinel is pushed to subscribers on Reset so the client can
// clear its accumulated view. A zero-width marker that can't appear in
// real log output.
const logResetSentinel = "\x00__reset__\x00"

// subscribe registers a log subscriber, returning its channel plus the
// current snapshot (so a fresh connection paints immediately) and an
// unsubscribe func.
func (l *logBuffer) subscribe() (initial string, ch <-chan string, unsubscribe func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.nextID
	l.nextID++
	c := make(chan string, logSubBuffer)
	l.subs[id] = c
	return string(l.buf), c, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if cc, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(cc)
		}
	}
}
