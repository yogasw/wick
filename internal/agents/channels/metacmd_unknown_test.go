package channels

import "testing"

// An unknown or abbreviated command is a message for the agent.
func TestParseMetaUnknownIsText(t *testing.T) {
	for _, in := range []string{"!s", "!stat", "!x", "/foo"} {
		if ParseMeta(in).IsMeta {
			t.Errorf("%q taken as a command", in)
		}
	}
}
