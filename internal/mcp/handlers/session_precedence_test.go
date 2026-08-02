package handlers

import "testing"

// The header is set per spawn by the runtime and cannot be forged by the
// model; a session_id argument is whatever the model typed. Connector ops
// act INSIDE the session they resolve — sub-agent delegation attaches to
// its tree, inherits its identity for tags, and reads roles from its
// project — so the header has to win, not merely fill a gap.
func TestResolveCallSession(t *testing.T) {
	tests := []struct {
		name   string
		header string
		arg    string
		want   string
	}{
		{"header wins over a conflicting argument", "sess-real", "sess-someone-else", "sess-real"},
		{"argument is used when no header is set", "", "sess-arg", "sess-arg"},
		{"header alone", "sess-real", "", "sess-real"},
		{"neither", "", "", ""},
		{"whitespace-only header does not mask the argument", "   ", "sess-arg", "sess-arg"},
		{"values are trimmed", " sess-real ", "", "sess-real"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveCallSession(tt.header, tt.arg); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
