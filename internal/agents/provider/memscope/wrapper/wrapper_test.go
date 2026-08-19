package wrapper

import (
	"strings"
	"testing"
)

// MemoryHigh throttles allocation instead of killing: a process past it
// stalls indefinitely while holding its slot. That turned one production
// incident into a 116-minute outage rather than a clean kill, so it is
// written at every ceiling — including none.
func TestRenderShim_AlwaysDisablesThrottlingAndSwap(t *testing.T) {
	for _, limit := range []int{0, 1200} {
		got := RenderShim(Provider{Name: "claude", RealBin: "/opt/x/claude", LimitMB: limit}, "agents.slice")
		if !strings.Contains(got, "MemoryHigh=infinity") {
			t.Errorf("limit %d: shim omits MemoryHigh=infinity", limit)
		}
		if !strings.Contains(got, "MemorySwapMax=0") {
			t.Errorf("limit %d: shim omits MemorySwapMax=0", limit)
		}
	}
}

// A zero ceiling is the measure-mode shape: the scope exists so a peak
// is readable, but nothing is ever killed for it. Writing MemoryMax=0
// would mean the opposite — a group that can hold nothing at all.
func TestRenderShim_ZeroLimitWritesNoCeiling(t *testing.T) {
	got := RenderShim(Provider{Name: "claude", RealBin: "/opt/x/claude"}, "agents.slice")

	if strings.Contains(got, "MemoryMax") {
		t.Fatalf("zero limit still wrote a ceiling:\n%s", got)
	}
}

func TestRenderShim_CarriesTheLimitAndRealBinary(t *testing.T) {
	got := RenderShim(Provider{Name: "codex", RealBin: "/home/u/.local/bin/codex", LimitMB: 1200}, "agents.slice")

	if !strings.Contains(got, "-p MemoryMax=1200M") {
		t.Errorf("shim lost the ceiling:\n%s", got)
	}
	if !strings.Contains(got, "REAL=/home/u/.local/bin/codex") {
		t.Errorf("shim lost the real binary:\n%s", got)
	}
	if !strings.Contains(got, "--slice=agents.slice") {
		t.Errorf("shim lost the slice:\n%s", got)
	}
}

// Both escape hatches keep a spawn working when isolation cannot be
// applied. An unguarded agent beats no agent at all -- the same trade
// memguard.go makes when a slice cannot be ensured.
func TestRenderShim_FallsThroughWhenIsolationIsUnavailable(t *testing.T) {
	got := RenderShim(Provider{Name: "claude", RealBin: "/opt/x/claude", LimitMB: 512}, "")

	if !strings.Contains(got, "AGENT_NO_CGROUP") {
		t.Error("no way to bypass the shim for one command")
	}
	if !strings.Contains(got, "XDG_RUNTIME_DIR") {
		t.Error("shim does not fall through without a user session")
	}
	if strings.Count(got, `exec "$REAL"`) < 2 {
		t.Errorf("expected both fallbacks to exec the real binary:\n%s", got)
	}
}

// An empty slice name must not produce `--slice=`, which systemd-run
// rejects.
func TestRenderShim_DefaultsTheSlice(t *testing.T) {
	got := RenderShim(Provider{Name: "claude", RealBin: "/opt/x/claude"}, "")

	if !strings.Contains(got, "--slice=agents.slice") {
		t.Fatalf("empty slice did not default:\n%s", got)
	}
}

// Restore must come before removing the shim. Reversed, the symlink
// points at a file that no longer exists and EVERY spawn fails -- worse
// than the state being undone.
func TestUnlinkCommands_RestoresTheRealBinary(t *testing.T) {
	cmds := UnlinkCommands(Provider{
		Name: "claude", RealBin: "/opt/x/claude", Link: "/usr/local/bin/claude",
	})

	if len(cmds) == 0 {
		t.Fatal("uninstall produced no commands")
	}
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "/opt/x/claude") || !strings.Contains(joined, "/usr/local/bin/claude") {
		t.Fatalf("restore does not point the link back at the real binary:\n%s", joined)
	}
}

// The existing entry is normally a symlink into a node install, and
// `npm i -g` will replace it again later -- so back it up before
// overwriting.
func TestLinkCommands_BacksUpBeforeOverwriting(t *testing.T) {
	cmds := LinkCommands(Provider{
		Name: "claude", RealBin: "/opt/x/claude", Link: "/usr/local/bin/claude",
	}, "/home/u/bin", "20260818-101500")

	if len(cmds) < 2 {
		t.Fatalf("expected a backup before the link, got %v", cmds)
	}
	if !strings.Contains(cmds[0], "cp -a") || !strings.Contains(cmds[0], "orig-20260818-101500") {
		t.Errorf("first command is not a stamped backup: %q", cmds[0])
	}
	if !strings.Contains(cmds[1], "ln -sfn") {
		t.Errorf("second command does not create the link: %q", cmds[1])
	}
	if !strings.Contains(cmds[1], "/home/u/bin/claude") {
		t.Errorf("link does not point at the shim: %q", cmds[1])
	}
}

// These commands are printed for a human to paste, so a path with a
// space must survive being pasted rather than becoming two arguments.
func TestLinkCommands_QuotesAwkwardPaths(t *testing.T) {
	cmds := LinkCommands(Provider{
		Name: "claude", RealBin: "/opt/my apps/claude", Link: "/usr/local/bin/claude",
	}, "/home/u/my bin", "20260818-101500")

	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, `'/home/u/my bin/claude'`) {
		t.Fatalf("shim path with a space was not quoted:\n%s", joined)
	}
}

// A process either has a ceiling over it or it does not. Who placed it
// there — wick at spawn, or the path shim — produces the identical
// cgroup, so the tally does not pretend to tell them apart.
func TestSummarize_CountsByWhetherACeilingApplies(t *testing.T) {
	got := Summarize([]ProcState{
		{PID: 1, Name: "claude", FromWick: true, Isolated: true},
		{PID: 2, Name: "claude", FromWick: true, Isolated: true},
		{PID: 3, Name: "claude", FromWick: true}, // wick's, but uncovered
		{PID: 4, Name: "claude"},                 // someone else's, uncovered
		{PID: 5, Name: "codex", Isolated: true},  // someone else's, covered
	})

	want := Summary{Total: 5, Isolated: 3, Unisolated: 2}
	if got != want {
		t.Fatalf("Summarize = %+v, want %+v", got, want)
	}
}

// Ownership stays on the row even though it no longer splits the count:
// knowing which agent to go look at is useful, it is just not what the
// tally is about.
func TestSummarize_OwnershipDoesNotChangeTheTally(t *testing.T) {
	wicks := Summarize([]ProcState{{PID: 1, FromWick: true, Isolated: true}})
	others := Summarize([]ProcState{{PID: 2, Isolated: true}})

	if wicks != others {
		t.Fatalf("same coverage counted differently by owner: %+v vs %+v", wicks, others)
	}
}

func TestSummarize_EmptyScanCountsNothing(t *testing.T) {
	if got := Summarize(nil); got != (Summary{}) {
		t.Fatalf("Summarize(nil) = %+v, want zero", got)
	}
}
