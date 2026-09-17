package agents

import "testing"

// Multipart is the path a message takes when a file is attached, and HTML
// form encoding rewrites its newlines to CRLF. The composer renders its own
// message optimistically and reconciles it with the persisted copy by text,
// so storing the CRLF form drew multi-line messages with an attachment twice
// until the page was reloaded.
func TestTrimFormTextNormalisesFormNewlines(t *testing.T) {
	cases := []struct{ in, want string }{
		{"line one\r\nline two", "line one\nline two"},
		{"  \r\n spaced \r\n\r\n", "spaced"},
		{"already\nplain", "already\nplain"},
		{"", ""},
		// A lone CR is not a form newline; leave the text as typed.
		{"carriage\rreturn", "carriage\rreturn"},
	}
	for _, c := range cases {
		if got := trimFormText(c.in); got != c.want {
			t.Errorf("trimFormText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
