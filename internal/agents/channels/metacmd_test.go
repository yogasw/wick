package channels

import (
	"testing"
)

func TestParseMeta(t *testing.T) {
	tests := []struct {
		input   string
		isMeta  bool
		cmd     string
		arg     string
	}{
		{"/dashboard", true, "dashboard", ""},
		{"/link", true, "dashboard", ""},
		{"!dashboard", true, "dashboard", ""},
		{"/reset", true, "reset", ""},
		{"/status", true, "status", ""},
		{"/agent backend", true, "agent", "backend"},
		{"/agent", true, "agent", ""},
		{"/log 20", true, "log", "20"},
		{"/log", true, "log", ""},
		{"hello world", false, "", ""},
		{"", false, "", ""},
		{"   ", false, "", ""},
		// Case insensitive
		{"/DASHBOARD", true, "dashboard", ""},
		{"/Reset", true, "reset", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseMeta(tc.input)
			if got.IsMeta != tc.isMeta {
				t.Errorf("IsMeta: got %v want %v", got.IsMeta, tc.isMeta)
			}
			if got.Cmd != tc.cmd {
				t.Errorf("Cmd: got %q want %q", got.Cmd, tc.cmd)
			}
			if got.Arg != tc.arg {
				t.Errorf("Arg: got %q want %q", got.Arg, tc.arg)
			}
		})
	}
}
