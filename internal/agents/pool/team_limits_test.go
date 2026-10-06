package pool

import (
	"slices"
	"strings"
	"testing"
)

func TestTeamLimitArgs(t *testing.T) {
	lim := TeamLimits{AgentID: "a1", DisallowedTools: []string{"Edit", "Write"}, BashAllowed: true, DisabledSkills: []string{"loki"}}

	args, hook := teamLimitArgs(lim, "/bin/gate", true, false, "/s/a1.json")
	if hook != "/bin/gate --spec /s/a1.json" {
		t.Fatalf("hook = %q", hook)
	}
	if len(args) != 2 || args[0] != "--disallowedTools" || args[1] != "Edit,Write,Skill(loki)" {
		t.Fatalf("args = %v", args)
	}

	// Gate on but its hook not installed: Bash would run unasked, so it is denied.
	args, hook = teamLimitArgs(lim, "/bin/gate", false, false, "")
	if hook != "/bin/gate" || !slices.Contains(strings.Split(args[1], ","), "Bash") {
		t.Fatalf("hook missing: hook=%q args=%v", hook, args)
	}

	// Gate off (bypass): Bash stays allowed, only the other switches are denied.
	args, hook = teamLimitArgs(lim, "/bin/gate", false, true, "")
	if hook != "/bin/gate" || len(args) != 2 || args[1] != "Edit,Write,Skill(loki)" {
		t.Fatalf("gate off: hook=%q args=%v", hook, args)
	}

	// Bash off: denied, and the hook keeps the shared spec.
	lim.BashAllowed = false
	args, hook = teamLimitArgs(lim, "/bin/gate", true, false, "/s/a1.json")
	if hook != "/bin/gate" || !strings.Contains(args[1], "BashOutput") {
		t.Fatalf("bash off: hook=%q args=%v", hook, args)
	}

	// Bash off stays off with the gate off too.
	args, _ = teamLimitArgs(lim, "/bin/gate", false, true, "")
	if !slices.Contains(strings.Split(args[1], ","), "Bash") {
		t.Fatalf("bash off, gate off: args=%v", args)
	}

	// Everything on: no flag at all.
	if args, _ := teamLimitArgs(TeamLimits{BashAllowed: true}, "/bin/gate", true, false, "/s"); args != nil {
		t.Fatalf("all on args = %v", args)
	}
}
