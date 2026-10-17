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

// The Done of an exit whose retries are spent carries its reason, so the
// task fails saying why; any other raw has none.
func TestExitGaveUp(t *testing.T) {
	if got := exitGaveUp(`{"type":"wick_process_exit","restarting":false,"reason":"stopped after 2 retries: no output for 20m"}`); got != "stopped after 2 retries: no output for 20m" {
		t.Errorf("got %q", got)
	}
	for _, raw := range []string{`{"type":"wick_process_exit","restarting":true}`, `{"type":"result","reason":"x"}`, ``} {
		if got := exitGaveUp(raw); got != "" {
			t.Errorf("%q: got %q", raw, got)
		}
	}
}
