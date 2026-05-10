package slack

import (
	"strings"
	"testing"
)

func TestChunkText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		max    int
		chunks int
	}{
		{"short", "hello", 3800, 1},
		{"exact", strings.Repeat("a", 3800), 3800, 1},
		{"one over", strings.Repeat("a", 3801), 3800, 2},
		{"double", strings.Repeat("a", 7600), 3800, 2},
		{"empty", "", 3800, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := chunkText(tc.input, tc.max)
			if len(got) != tc.chunks {
				t.Errorf("got %d chunks, want %d", len(got), tc.chunks)
			}
			// Verify no data is lost.
			var joined string
			for _, c := range got {
				joined += c
			}
			if joined != tc.input {
				t.Errorf("chunks do not reassemble to original input")
			}
		})
	}
}

func TestChunkTextBreaksOnNewline(t *testing.T) {
	// Build a string that has a newline near the boundary so the chunker
	// should prefer to break there.
	near := strings.Repeat("a", 3750) + "\n" + strings.Repeat("b", 100)
	chunks := chunkText(near, 3800)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if strings.Contains(chunks[0], "b") {
		t.Error("first chunk should not contain 'b' content after newline break")
	}
}

func TestAllowed(t *testing.T) {
	s := &Channel{}

	// everyone
	s.cfg.AccessMode = "everyone"
	if !s.allowedCfg(s.cfg, "U123", nil) {
		t.Error("everyone mode: should allow any user")
	}

	// users
	s.cfg.AccessMode = "users"
	s.cfg.AllowedUsers = "U001\nU002"
	if !s.allowedCfg(s.cfg, "U001", nil) {
		t.Error("users mode: U001 should be allowed")
	}
	if s.allowedCfg(s.cfg, "U999", nil) {
		t.Error("users mode: U999 should be denied")
	}

	// groups
	s.cfg.AccessMode = "groups"
	s.cfg.AllowedGroups = "G001\nG002"
	if !s.allowedCfg(s.cfg, "Uany", []string{"G001"}) {
		t.Error("groups mode: member of G001 should be allowed")
	}
	if s.allowedCfg(s.cfg, "Uany", []string{"G999"}) {
		t.Error("groups mode: member of G999 should be denied")
	}
	if s.allowedCfg(s.cfg, "Uany", nil) {
		t.Error("groups mode: no groups should be denied")
	}
}

func TestDashboardURL(t *testing.T) {
	s := &Channel{pubURL: "https://wick.example.com"}
	got := s.dashboardURL("1715167891.234567")
	want := "https://wick.example.com/tools/agents/sessions/1715167891.234567"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}

	// Empty pubURL
	s.pubURL = ""
	got = s.dashboardURL("T123")
	if !strings.Contains(got, "not configured") {
		t.Errorf("expected 'not configured' message, got %q", got)
	}
}
