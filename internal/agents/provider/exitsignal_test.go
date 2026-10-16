package provider

import "testing"

func TestExitSignalName(t *testing.T) {
	cases := []struct {
		code    int
		waitErr string
		want    string
	}{
		{143, "exit status 143", "SIGTERM"},
		{137, "exit status 137", "SIGKILL"},
		{-1, "signal: terminated", "SIGTERM"},
		{-1, "signal: killed", "SIGKILL"},
		{1, "exit status 1", ""},
		{0, "", ""},
	}
	for _, c := range cases {
		if got := exitSignalName(c.code, c.waitErr); got != c.want {
			t.Errorf("exitSignalName(%d, %q) = %q, want %q", c.code, c.waitErr, got, c.want)
		}
	}
}

func TestExitSignalNote(t *testing.T) {
	cases := []struct {
		sig, cause, want string
	}{
		{"SIGTERM", "", "SIGTERM, dihentikan dari luar wick (bukan Stop/Kill wick): tidak ada Stop/Kill dari wick yang tercatat"},
		{"SIGKILL", "idle timer", "SIGKILL, dihentikan oleh wick: idle timer"},
		{"", "Stop()", "dihentikan oleh wick: Stop()"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := exitSignalNote(c.sig, c.cause); got != c.want {
			t.Errorf("exitSignalNote(%q, %q) = %q, want %q", c.sig, c.cause, got, c.want)
		}
	}
}

// The noted cause belongs to the process it was noted for, and the first
// (more specific) note for a process is kept.
func TestNoteStopCauseIsPerProcess(t *testing.T) {
	a := &Agent{}
	if got := a.wickStopCause(0); got != "" {
		t.Fatalf("fresh agent cause = %q, want empty", got)
	}
	a.NoteStopCause("KillAgent by leader")
	a.NoteStopCause("Stop()")
	if got := a.wickStopCause(0); got != "KillAgent by leader" {
		t.Fatalf("cause = %q, want the first note kept", got)
	}
	if got := a.wickStopCause(4242); got != "" {
		t.Fatalf("cause for another pid = %q, want empty", got)
	}
}
