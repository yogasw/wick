package main

import "testing"

// Only status and diff run with optional locks off; everything else keeps
// git's normal locking.
func TestCmdReadsOnly(t *testing.T) {
	for args, want := range map[string]bool{
		"status":     true,
		"diff":       true,
		"add":        false,
		"commit":     false,
		"log":        false,
		"--no-pager": false,
	} {
		c := Cmd{UserArgs: []string{args, "x"}}
		if args == "--no-pager" {
			c.UserArgs = []string{"--no-pager"}
		}
		if got := c.readsOnly(); got != want {
			t.Errorf("readsOnly(%q) = %v, want %v", args, got, want)
		}
	}
	if !(Cmd{UserArgs: []string{"--no-pager", "diff"}}).readsOnly() {
		t.Error("a leading flag must not hide the subcommand")
	}
}
