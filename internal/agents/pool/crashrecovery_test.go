package pool

import (
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// Only genuinely unexplained deaths warrant a restart. Every other reason
// already has an explanation, and restarting would fight whoever caused
// it — or repeat the work that killed the process.
func TestShouldRespawn_OnlyUnexplainedDeaths(t *testing.T) {
	cases := []struct {
		reason provider.ExitReason
		want   bool
		why    string
	}{
		{provider.ExitError, true, "a crash has no explanation — worth one more try"},
		{provider.ExitClean, false, "it finished; restarting would redo the work"},
		{provider.ExitStopped, false, "someone asked for this — preempt, session change, shutdown"},
		{provider.ExitIdle, false, "the idle TTL reclaimed it on purpose"},
		{provider.ExitRespawn, false, "an internal turn boundary, not a death"},
		{provider.ExitOOM, false, "repeating the work would hit the same ceiling"},
	}
	for _, c := range cases {
		if got := ShouldRespawn(c.reason, 0); got != c.want {
			t.Fatalf("ShouldRespawn(%v) = %v, want %v — %s",
				c.reason, got, c.want, c.why)
		}
	}
}

// The cap is the whole point: a process that dies immediately on start
// will die again, and unlimited retries turn one broken configuration
// into an infinite loop.
func TestShouldRespawn_StopsAtTheCap(t *testing.T) {
	for i := 0; i < maxRespawnAttempts; i++ {
		if !ShouldRespawn(provider.ExitError, i) {
			t.Fatalf("attempt %d refused while under the cap of %d", i, maxRespawnAttempts)
		}
	}
	if ShouldRespawn(provider.ExitError, maxRespawnAttempts) {
		t.Fatalf("attempt %d allowed past the cap of %d", maxRespawnAttempts, maxRespawnAttempts)
	}
}

// An agent that crashed once an hour ago is not in a crash loop and
// should get its full budget back.
func TestNoteCrash_WindowResets(t *testing.T) {
	p := &Pool{}
	t0 := time.Unix(1_700_000_000, 0)

	if n := p.noteCrashLocked("s/a", t0); n != 1 {
		t.Fatalf("first crash counted as %d, want 1", n)
	}
	if n := p.noteCrashLocked("s/a", t0.Add(time.Minute)); n != 2 {
		t.Fatalf("second crash in-window counted as %d, want 2", n)
	}

	// Past the window: the old failures no longer count against it.
	if n := p.noteCrashLocked("s/a", t0.Add(respawnWindow+time.Second)); n != 1 {
		t.Fatalf("crash after the window counted as %d, want 1 (counter should reset)", n)
	}
}

// Crash counts are per agent: one flaky session must not consume the
// budget of an unrelated one.
func TestNoteCrash_IsPerAgent(t *testing.T) {
	p := &Pool{}
	now := time.Unix(1_700_000_000, 0)

	p.noteCrashLocked("s1/a", now)
	p.noteCrashLocked("s1/a", now)
	if n := p.noteCrashLocked("s2/b", now); n != 1 {
		t.Fatalf("unrelated agent started at %d, want 1", n)
	}
}

// A clean stop must not leave a stale counter that shortens the budget of
// a future, unrelated crash.
func TestClearCrashes(t *testing.T) {
	p := &Pool{}
	now := time.Unix(1_700_000_000, 0)

	p.noteCrashLocked("s/a", now)
	p.noteCrashLocked("s/a", now)
	p.clearCrashesLocked("s/a")

	if n := p.noteCrashLocked("s/a", now); n != 1 {
		t.Fatalf("after clearing, next crash counted as %d, want 1", n)
	}
}

// The agent resumes with its conversation intact, so from the inside a
// crash is invisible — the last thing it did just produced nothing. The
// notice has to say what happened AND what to do, or the agent either
// goes silent or starts the task over.
func TestCrashNotice_TellsTheAgentWhatToDo(t *testing.T) {
	got := crashNotice("exit status 137", 1, false)

	low := strings.ToLower(got)
	for _, want := range []string{"restarted", "exit status 137"} {
		if !strings.Contains(low, strings.ToLower(want)) {
			t.Fatalf("notice %q does not mention %q", got, want)
		}
	}
	// The instruction that matters: continue, do not restart the task.
	if !strings.Contains(low, "do not start the task over") {
		t.Fatalf("notice %q does not tell the agent to continue rather than restart", got)
	}
}

// Once the cap is reached the message must change: promising another
// restart that will not come would leave the agent waiting.
func TestCrashNotice_GiveUpIsDifferent(t *testing.T) {
	retry := crashNotice("signal: killed", 1, false)
	final := crashNotice("signal: killed", 3, true)

	if retry == final {
		t.Fatal("the give-up notice is identical to the retry notice")
	}
	if !strings.Contains(strings.ToLower(final), "not restarted again") {
		t.Fatalf("give-up notice %q does not say restarting has stopped", final)
	}
}

// An OOM is not retried, and the agent needs a different instruction: the
// same work would hit the same ceiling, so it must change approach.
func TestOOMNotice_AsksForASmallerApproach(t *testing.T) {
	got := oomNotice("used 1.6 GB, over its 1.5 GB limit.")

	low := strings.ToLower(got)
	if !strings.Contains(low, "not restarted") {
		t.Fatalf("notice %q does not say it was left stopped", got)
	}
	if !strings.Contains(low, "smaller") {
		t.Fatalf("notice %q does not ask for a smaller approach", got)
	}
	if !strings.Contains(got, "1.6 GB") {
		t.Fatalf("notice %q dropped the measured figure", got)
	}
	// The detail's settings advice ("raise the limit") is addressed to the
	// operator — the agent cannot change settings. The notice must tell
	// the agent to relay that advice, not act on it.
	if !strings.Contains(low, "relay") {
		t.Fatalf("notice %q does not tell the agent to relay the settings advice", got)
	}
}

// The restart path re-enters the pool through Send, which can spawn,
// which can fail, which calls back here. The counter is the only thing
// standing between that and unbounded recursion — so it must reach the
// cap after exactly maxRespawnAttempts crashes, with no off-by-one that
// would let one extra level through.
func TestRespawnBudget_TerminatesTheRecursion(t *testing.T) {
	p := &Pool{}
	now := time.Unix(1_700_000_000, 0)

	allowed := 0
	for i := 0; i < 20; i++ {
		attempt := p.noteCrashLocked("s/a", now)
		// Mirrors recoverFromExit: attempt-1 is how many came before.
		if !ShouldRespawn(provider.ExitError, attempt-1) {
			break
		}
		allowed++
	}

	if allowed != maxRespawnAttempts {
		t.Fatalf("restart loop allowed %d attempts, want exactly %d — the budget does not bound it",
			allowed, maxRespawnAttempts)
	}
}
