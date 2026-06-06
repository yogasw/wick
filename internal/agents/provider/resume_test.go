package provider

import "testing"

func TestIsResumeNotFound(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"stderr: No conversation found with session ID: 84f1648a", true},
		{"no conversation found", true},
		{"NO CONVERSATION FOUND with id", true},
		{"error_during_execution: rate limited", false},
		{"some unrelated failure", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsResumeNotFound(c.in); got != c.want {
			t.Errorf("IsResumeNotFound(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
