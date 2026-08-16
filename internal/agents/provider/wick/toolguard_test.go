package wick

import (
	"strings"
	"testing"
)

// A zero limit means the operator turned tool guarding off (the default);
// the command must run exactly as it did before the guard existed.
func TestWrapToolCmd_ZeroLimitDoesNotWrap(t *testing.T) {
	bin, args, unit := wrapToolCmd("/bin/sh", []string{"-c", "grep -r x ."}, 0, 1)
	if bin != "/bin/sh" || unit != "" {
		t.Fatalf("bin=%q unit=%q, want the command untouched", bin, unit)
	}
	if len(args) != 2 || args[1] != "grep -r x ." {
		t.Fatalf("args = %v, want them untouched", args)
	}
}

// Scope names must not collide: systemd refuses a duplicate unit name
// while the first is alive, and background shells outlive each other.
func TestNextToolSeq_Unique(t *testing.T) {
	if nextToolSeq() == nextToolSeq() {
		t.Fatal("tool scope sequence repeated")
	}
}

// An unwrapped command is never an OOM — there is no scope to have been
// killed, and claiming otherwise would mislabel every ordinary failure.
func TestToolOOMKilled_UnwrappedIsNeverOOM(t *testing.T) {
	if toolOOMKilled("") {
		t.Fatal("an unwrapped command reported an OOM kill")
	}
}

// The message must tell the model what to do differently, because the
// model is the one that decides whether to retry with a narrower scope.
// A bare "command failed" gets retried unchanged.
func TestToolOOMMessage_IsActionable(t *testing.T) {
	got := toolOOMMessage(512)
	if !strings.Contains(got, "512 MB") {
		t.Fatalf("message %q does not name the limit", got)
	}
	low := strings.ToLower(got)
	if !strings.Contains(low, "narrow") && !strings.Contains(low, "smaller") {
		t.Fatalf("message %q does not suggest a way forward", got)
	}
}
