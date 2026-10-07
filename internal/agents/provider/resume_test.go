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
		{`Error: Session "01a0f26e-9894" not found`, true},
		{"Error: Session not found", true},
		{`session "x" started`, false},
		{`session "s1" could not load: model "x" not found`, false},
		{"session \"s1\" started\nerror: file \"a.txt\" not found", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsResumeNotFound(c.in); got != c.want {
			t.Errorf("IsResumeNotFound(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
