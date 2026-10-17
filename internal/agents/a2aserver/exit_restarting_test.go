package a2aserver

import "testing"

// The Done wick publishes for a process exit it is restarting keeps the
// task working; any other Done ends it.
func TestExitRestarting(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"type":"wick_process_exit","restarting":true}`, true},
		{`{"type":"wick_process_exit","restarting":false}`, false},
		{`{"type":"result","subtype":"success"}`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := exitRestarting(c.raw); got != c.want {
			t.Errorf("%q: got %v, want %v", c.raw, got, c.want)
		}
	}
}
