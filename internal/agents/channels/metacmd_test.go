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
		{"!help", true, "help", ""},
		{"!help me", false, "", ""},
		{"/link", true, "dashboard", ""},
		{"!dashboard", true, "dashboard", ""},
		{"/reset", true, "reset", ""},
		{"/status", true, "status", ""},
		{"/agent backend", true, "agent", "backend"},
		{"/agent", true, "agent", ""},
		{"/log 20", true, "log", "20"},
		{"/log", true, "log", ""},
		{"hello world", false, "", ""},
		// Only a prefixed word is a command; a bare word is a message.
		{"!link", true, "dashboard", ""},
		{"!log 20", true, "log", "20"},
		{"link", false, "", ""},
		{"status", false, "", ""},
		{"dashboard", false, "", ""},
		{"!link href nya dong", false, "", ""},
		{"/status please", false, "", ""},
		{"!reset password user", false, "", ""},
		{"link href nya dong, biar ngak perlu copy paste", false, "", ""},
		{"status deploy tadi gimana", false, "", ""},
		{"log error kemarin kenapa", false, "", ""},
		{"agent mana yang jawab", false, "", ""},
		{"reset password user itu", false, "", ""},
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
