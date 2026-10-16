package slack

import "testing"

// In Slack only "!" makes a command; a sentence that starts with a command
// word, or a bare word, is a message for the agent.
func TestSlackMetaNeedsBang(t *testing.T) {
	for in, want := range map[string]bool{
		"!link":               true,
		"!dashboard":          true,
		"  !status":           true,
		"link":                false,
		"link href nya dong":  false,
		"status deploy tadi":  false,
		"!link href nya dong": false,
		"!status deploy tadi": false,
		"!log 20":             true,
		"/link":               false,
		"hello":               false,
	} {
		if got := slackMeta(in).IsMeta; got != want {
			t.Errorf("slackMeta(%q).IsMeta = %v, want %v", in, got, want)
		}
	}
}
