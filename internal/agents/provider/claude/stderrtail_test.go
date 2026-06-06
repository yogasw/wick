package claude

import (
	"strings"
	"testing"
)

func TestStderrTailKeepsLastBytes(t *testing.T) {
	s := newStderrTail(10)
	_, _ = s.Write([]byte("0123456789ABCDEF"))
	got := s.String()
	if got != "6789ABCDEF" {
		t.Fatalf("got %q, want last 10 bytes %q", got, "6789ABCDEF")
	}
}

func TestStderrTailTrimsAndUnderMax(t *testing.T) {
	s := newStderrTail(4096)
	_, _ = s.Write([]byte("  No conversation found with session ID: abc\n"))
	if got := s.String(); !strings.Contains(got, "No conversation found") || strings.HasSuffix(got, "\n") {
		t.Fatalf("got %q, want trimmed content", got)
	}
}

func TestStderrTailNilSafe(t *testing.T) {
	var s *stderrTail
	if s.String() != "" {
		t.Fatal("nil tail should stringify empty")
	}
}
