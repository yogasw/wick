package view

import (
	"strings"
	"testing"
)

// A conversation whose own process has gone idle but whose sub-agent is
// still grinding away used to render an idle dot — indistinguishable from
// a conversation with nothing running at all. The sidebar is the only
// place liveness is visible once you navigate away from a chat, so a
// child's work has to reach its parent's row.
func TestEffectiveStatusRollsUpSubAgentWork(t *testing.T) {
	cases := []struct {
		name      string
		lifecycle string
		status    string
		sub       string
		want      string
	}{
		// The leader owns the row whenever it is doing anything itself:
		// "my process is working" is a stronger statement than "something
		// downstream is", and conflating them would hide the difference.
		{"leader working wins over child", "working", "", "working", "working"},
		{"leader spawning wins over child", "spawning", "", "working", "spawning"},

		// The case this test exists for.
		{"idle leader with working child", "idle", "", "working", "subagent"},
		{"idle leader with spawning child", "idle", "", "spawning", "subagent"},
		{"killed leader with working child", "killed", "", "working", "subagent"},
		{"no leader process, working child", "", "", "working", "subagent"},

		// A queued child has nothing running yet. Reporting it as busy
		// would promise progress that is not happening — the same reason
		// isSubAgentWorking refuses to spin on a queued row.
		{"queued child is not work", "idle", "", "queued", "idle"},
		{"no child work leaves leader alone", "idle", "", "", "idle"},
		{"queued leader still shows", "", "queued", "", "queued"},
		{"nothing at all", "", "", "", ""},
		{"killed leader alone stays blank", "killed", "", "", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectiveStatus(tc.lifecycle, tc.status, tc.sub); got != tc.want {
				t.Errorf("effectiveStatus(%q, %q, %q) = %q, want %q",
					tc.lifecycle, tc.status, tc.sub, got, tc.want)
			}
		})
	}
}

// The dot has to be visibly different for delegated work, not just
// present: a green spinner on the parent row would say "this chat is
// working" when the chat itself is idle.
func TestStatusDotClassDistinguishesSubAgent(t *testing.T) {
	leader := StatusDotClass("working")
	sub := StatusDotClass("subagent")

	if !strings.Contains(sub, "animate-spin") {
		t.Errorf("sub-agent dot must spin, got %q", sub)
	}
	if sub == leader {
		t.Error("sub-agent dot is identical to the leader's — the row cannot say whose work it is")
	}
	if strings.Contains(sub, "border-green-500") {
		t.Errorf("sub-agent dot reuses the leader's green, got %q", sub)
	}
	// Still a dot on the same 12px geometry as the other spinners, so the
	// rows do not jump when a status changes.
	if !strings.Contains(sub, "h-3 w-3") {
		t.Errorf("sub-agent dot changed size, got %q", sub)
	}
	if strings.Contains(StatusDotClass(""), "animate-spin") {
		t.Error("empty status must not spin")
	}
	if !strings.Contains(StatusDotClass(""), "hidden") {
		t.Error("empty status must stay hidden")
	}
}
