package agents

import "testing"

// A shell script writes the word that comes naturally — "done", "ok",
// "running". Rejecting those mid-build means a report nobody gets, so the
// vocabulary is mapped rather than policed.
func TestNormalizeTodoStatusAcceptsScriptWords(t *testing.T) {
	cases := map[string]string{
		"done": "completed", "ok": "completed", "PASSED": "completed",
		"running": "in_progress", "in-progress": "in_progress", "started": "in_progress",
		"failed": "failed", "error": "failed",
		"killed": "stopped", "cancelled": "stopped",
		"": "", "something else": "pending",
	}
	for in, want := range cases {
		if got := normalizeTodoStatus(in); got != want {
			t.Errorf("normalizeTodoStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// The renderer's vocabulary stays closed: anything the panel cannot draw is
// shown as text, which is always readable, rather than passed through to a
// branch that does not exist.
func TestNormalizeTodoFormatFallsBackToText(t *testing.T) {
	cases := map[string]string{
		"md": "markdown", "markdown": "markdown",
		"JSON": "json", "html": "html", "xml": "xml",
		"": "text", "yaml": "text", "application/json": "text",
	}
	for in, want := range cases {
		if got := normalizeTodoFormat(in); got != want {
			t.Errorf("normalizeTodoFormat(%q) = %q, want %q", in, got, want)
		}
	}
}
