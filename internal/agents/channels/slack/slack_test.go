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

	// default — all modes "all", any user / group / channel passes
	s.cfg.UsersMode = "all"
	s.cfg.GroupsMode = "all"
	s.cfg.ChannelsMode = "all"
	if !s.allowedCfg(s.cfg, "U123", nil, "C123") {
		t.Error("all mode: should allow any user")
	}

	// users whitelist
	s.cfg.UsersMode = "whitelist"
	s.cfg.AllowedUsers = `[{"id":"U001","name":"a"},{"id":"U002","name":"b"}]`
	if !s.allowedCfg(s.cfg, "U001", nil, "C1") {
		t.Error("users whitelist: U001 should be allowed")
	}
	if s.allowedCfg(s.cfg, "U999", nil, "C1") {
		t.Error("users whitelist: U999 should be denied")
	}

	// groups whitelist (users back to all)
	s.cfg.UsersMode = "all"
	s.cfg.GroupsMode = "whitelist"
	s.cfg.AllowedGroups = `[{"id":"G001","name":"g1"},{"id":"G002","name":"g2"}]`
	if !s.allowedCfg(s.cfg, "Uany", []string{"G001"}, "C1") {
		t.Error("groups whitelist: member of G001 should be allowed")
	}
	if s.allowedCfg(s.cfg, "Uany", []string{"G999"}, "C1") {
		t.Error("groups whitelist: member of G999 should be denied")
	}
	if s.allowedCfg(s.cfg, "Uany", nil, "C1") {
		t.Error("groups whitelist: no groups should be denied")
	}

	// users + groups whitelist (OR): pass via users
	s.cfg.UsersMode = "whitelist"
	s.cfg.AllowedUsers = `[{"id":"U001","name":"a"}]`
	s.cfg.GroupsMode = "whitelist"
	s.cfg.AllowedGroups = `[{"id":"G001","name":"g1"}]`
	if !s.allowedCfg(s.cfg, "U001", nil, "C1") {
		t.Error("OR semantic: U001 in users whitelist should pass even with no group")
	}
	// pass via groups
	if !s.allowedCfg(s.cfg, "U999", []string{"G001"}, "C1") {
		t.Error("OR semantic: member of G001 should pass even when not in users whitelist")
	}
	// neither matches
	if s.allowedCfg(s.cfg, "U999", []string{"G999"}, "C1") {
		t.Error("OR semantic: no match in users or groups should be denied")
	}

	// channels whitelist
	s.cfg.UsersMode = "all"
	s.cfg.GroupsMode = "all"
	s.cfg.ChannelsMode = "whitelist"
	s.cfg.AllowedChannels = `[{"id":"CABC","name":"#general"}]`
	if !s.allowedCfg(s.cfg, "U1", nil, "CABC") {
		t.Error("channels whitelist: CABC should be allowed")
	}
	if s.allowedCfg(s.cfg, "U1", nil, "CXYZ") {
		t.Error("channels whitelist: CXYZ should be denied")
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
