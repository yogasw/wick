//go:build linux || android

package memreport

import "testing"

// /proc/<pid>/cmdline separates argv with NUL bytes and usually ends with
// one. Converting it naively yields a line with embedded NULs that renders
// as a single run-together word — the opposite of the readability this
// field exists for.
func TestParseCmdline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"argv joined with spaces", "node\x00server.js\x00--port\x008080\x00", "node server.js --port 8080"},
		{"single argument", "/usr/bin/gopls\x00", "/usr/bin/gopls"},
		{"no trailing NUL", "grep\x00-r\x00foo", "grep -r foo"},
		// Kernel threads have an empty cmdline; the caller falls back to
		// the process name rather than rendering a blank row.
		{"kernel thread", "", ""},
		{"only NULs", "\x00\x00", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCmdline(c.in); got != c.want {
				t.Fatalf("parseCmdline(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
