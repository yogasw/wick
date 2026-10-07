package pool

import (
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// crashrecovery.go decides what to do when an agent's subprocess dies
// without anyone asking it to.
//
// Death is already detected (onAgentExit, ReconcileDead) — but detection
// only released the slot. From the session's point of view the agent
// simply stopped mid-sentence: no explanation, no continuation, and the
// next message started a fresh process with no idea that anything had
// gone wrong.
//
// Two things are needed, and they are separate decisions:
//
//   - whether to start it again at all (this file), and
//   - what to tell the agent once it is back (crashNotice below).

// maxRespawnAttempts bounds automatic restarts within one window.
//
// A cap rather than unlimited retries because the failure that most often
// looks recoverable — a process that dies immediately on start — is
// exactly the one that will die again. Three attempts distinguishes a
// transient fault from a broken configuration without turning the second
// into an infinite loop.
const maxRespawnAttempts = 3

// respawnWindow is how long attempts accumulate before the counter
// resets. An agent that crashed once an hour ago is not in a crash loop,
// and should get its full budget again.
const respawnWindow = 10 * time.Minute

// crashState tracks recent unexplained deaths for one agent.
//
// halted latches once the budget is spent. It exists because giving up is
// not a one-off decision: the process keeps dying for as long as whatever
// broke it stays broken, and every one of those deaths re-enters
// recoverFromExit. Without the latch the pool announces the same verdict
// on every crash, filling the transcript with identical notices.
type crashState struct {
	attempts int
	first    time.Time
	halted   bool
}

// ShouldRespawn reports whether an exit warrants an automatic restart,
// given how many attempts have already been made in the current window.
//
// Deliberately narrow. Only deaths with NO good explanation qualify:
//
//   - ExitClean: the process finished its work. Restarting would run it
//     again for no reason.
//   - ExitStopped: someone asked for this — a preempt, a session change,
//     or a shutdown. Restarting would fight the operator.
//   - ExitIdle: the idle TTL reclaimed it on purpose; the next message
//     spawns a fresh one anyway.
//   - ExitRespawn: an internal turn boundary, not a death at all.
//   - ExitOOM: the agent exceeded its memory limit. Restarting repeats
//     the work that caused it — the loop this whole subsystem exists to
//     prevent. The operator gets told instead.
//
// That leaves ExitError: a crash, a signal, a vanished process. Those are
// the ones worth trying again.
func ShouldRespawn(reason provider.ExitReason, attempts int) bool {
	if reason != provider.ExitError {
		return false
	}
	return attempts < maxRespawnAttempts
}

// noteCrash records an unexplained death and returns the attempt number
// this exit represents (1 for the first in a window).
//
// Caller MUST hold p.mu.
func (p *Pool) noteCrashLocked(key string, now time.Time) int {
	if p.crashes == nil {
		p.crashes = map[string]*crashState{}
	}
	st, ok := p.crashes[key]
	if !ok || now.Sub(st.first) > respawnWindow {
		// First crash, or the old window has expired — start fresh rather
		// than holding an hour-old failure against a healthy agent.
		st = &crashState{first: now}
		p.crashes[key] = st
	}
	st.attempts++
	return st.attempts
}

// markHaltedLocked latches the give-up verdict and reports whether this
// call is the one that set it. Only the first gets to announce.
//
// Caller MUST hold p.mu.
func (p *Pool) markHaltedLocked(key string) bool {
	st, ok := p.crashes[key]
	if !ok {
		return false
	}
	if st.halted {
		return false
	}
	st.halted = true
	return true
}

// clearCrashesLocked forgets an agent's crash history. Called when it
// exits for an explained reason, so a clean stop does not leave a stale
// counter that shortens the budget of a future crash.
//
// Caller MUST hold p.mu.
func (p *Pool) clearCrashesLocked(key string) {
	delete(p.crashes, key)
}

// crashNotice is what the agent is told after being brought back.
//
// The agent resumes with its conversation intact (--resume), so without
// this it has no way to know a process boundary happened at all: from
// inside, the last thing it did simply produced no result. That leads to
// either silence or starting the task over.
//
// The message states the cause, that work may be half-finished, and what
// to do — continue rather than restart. It is addressed to the agent, not
// the user, because the agent is the one that has to act on it.
func crashNotice(reason string, attempt int) string {
	return fmt.Sprintf(
		"[system] Your process stopped unexpectedly (%s) and was restarted — this is attempt %d. "+
			"Your conversation is intact, but any work in flight may be half-finished: "+
			"a file may be partly written, a command may have run without you seeing its output. "+
			"Check the state of whatever you were doing before continuing, and carry on from there — "+
			"do not start the task over from the beginning.",
		reason, attempt)
}

// haltNotice is what the SESSION is told once the restart budget is spent.
//
// Addressed to the person, not the agent, and that is the whole point: the
// agent is not coming back to read it. A process that dies three times in a
// row dies because of something outside the conversation — a provider
// config that no longer parses, a CLI that cannot start — and only a human
// can fix that. It names the cause and the one action that resumes work.
//
// It is delivered as a buffered system turn (no spawn), so it also becomes
// leading context for whatever agent the next message does start.
func haltNotice(reason string, attempts int) string {
	return fmt.Sprintf(
		"[system] The agent process stopped unexpectedly %d times in a row (%s) and was NOT restarted again. "+
			"Repeated instant exits almost always mean something outside the conversation is broken — "+
			"a provider config file that no longer parses, a CLI binary that cannot start, a bad model or "+
			"credential setting. Nothing further will be retried automatically: fix the cause, or switch "+
			"provider, then send a message to start the agent again.",
		attempts, reason)
}

// oomNotice explains a memory kill, which is NOT retried.
//
// Separate from crashNotice because the remedy is different and the agent
// can act on it: the same work would hit the same ceiling, so it has to
// change approach rather than try again.
func oomNotice(detail string) string {
	return "[system] Your process was stopped for using too much memory: " + detail +
		" It was NOT restarted automatically, because repeating the same work would hit the same limit. " +
		"Any settings advice in that message is for the user, not you — relay it to them. " +
		"Tell them what you were doing, and suggest a smaller way to do it yourself " +
		"(fewer files at once, streaming instead of loading everything, or a narrower scope)."
}
