package router9

import (
	"bytes"
	"sync"
)

// logBuffer is a thread-safe, bounded sink for the 9router process's
// stdout+stderr. It satisfies io.Writer (passed as cmd.Stdout/Stderr)
// and keeps only the most recent maxBytes so a long-running process
// can't grow it without bound. Snapshot returns the current contents
// for the logs endpoint.
type logBuffer struct {
	mu  sync.Mutex
	buf []byte
}

// maxLogBytes caps the retained log tail (~64 KiB is plenty for a
// glance; the full stream still goes nowhere else, by design).
const maxLogBytes = 64 * 1024

func newLogBuffer() *logBuffer { return &logBuffer{} }

// Write appends p, trimming from the front to stay under maxLogBytes.
// Trimming snaps to the next newline so we never show a half line.
func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	if len(l.buf) > maxLogBytes {
		over := len(l.buf) - maxLogBytes
		if nl := bytes.IndexByte(l.buf[over:], '\n'); nl >= 0 {
			over += nl + 1
		}
		l.buf = append(l.buf[:0], l.buf[over:]...)
	}
	return len(p), nil
}

// Snapshot returns a copy of the retained log tail.
func (l *logBuffer) Snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return string(l.buf)
}

// Reset clears the buffer — called on each (re)start so the panel shows
// only the current process's output.
func (l *logBuffer) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = l.buf[:0]
}
