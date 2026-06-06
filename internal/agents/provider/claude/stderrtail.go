package claude

import (
	"strings"
	"sync"
)

// stderrTail is an io.Writer that retains only the last maxBytes written
// to it. The agent reader logs this on an abnormal subprocess exit so an
// operator sees claude's real failure (e.g. "No conversation found with
// session ID: ...") instead of reconstructing the spawn by hand.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newStderrTail(maxBytes int) *stderrTail {
	if maxBytes <= 0 {
		maxBytes = 4096
	}
	return &stderrTail{max: maxBytes}
}

func (s *stderrTail) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	if len(s.buf) > s.max {
		s.buf = s.buf[len(s.buf)-s.max:]
	}
	return len(p), nil
}

func (s *stderrTail) String() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.buf))
}
