package slack

import (
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// sessionsWithBinding is a SessionChecker that only knows one binding.
type sessionsWithBinding struct {
	binding agentchannels.ThreadBinding
	has     bool
	wrote   []agentchannels.ThreadBinding
}

func (f *sessionsWithBinding) SessionExists(string) bool { return true }
func (f *sessionsWithBinding) AutoReplyOn(string) bool   { return false }
func (f *sessionsWithBinding) SetAutoReply(string, bool) {}
func (f *sessionsWithBinding) ThreadBinding(string) (agentchannels.ThreadBinding, bool) {
	return f.binding, f.has
}
func (f *sessionsWithBinding) SetThreadBinding(_ string, b agentchannels.ThreadBinding) {
	f.wrote = append(f.wrote, b)
}

// The turns map is filled by the code that RECEIVES a Slack message. A reply
// typed in the web UI, or a session a workflow created before a handover, has
// no entry — and every delivery path then no-ops, so the agent answers into
// nothing. The persisted binding is what brings the thread back.
func TestEnsureTurnRestoresFromBinding(t *testing.T) {
	sess := &sessionsWithBinding{
		binding: agentchannels.ThreadBinding{
			Channel: "slack", ChatID: "C0BD4UHBQRJ", ThreadID: "1789014375.645429",
		},
		has: true,
	}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}

	got := c.ensureTurn("slack-owner-1789014375.645429")
	if got == nil {
		t.Fatal("turn not restored")
	}
	if got.channelID != "C0BD4UHBQRJ" || got.threadTS != "1789014375.645429" {
		t.Fatalf("restored the wrong thread: channel=%q thread=%q", got.channelID, got.threadTS)
	}
	c.mu.Lock()
	_, stored := c.turns["slack-owner-1789014375.645429"]
	c.mu.Unlock()
	if !stored {
		t.Error("restored turn should be cached in the map")
	}
}

// A binding written before ThreadID was recorded still identifies the thread:
// the session key IS the prefix plus the thread ts.
func TestEnsureTurnDerivesThreadFromSessionKey(t *testing.T) {
	sess := &sessionsWithBinding{
		binding: agentchannels.ThreadBinding{Channel: "slack", ChatID: "C123"},
		has:     true,
	}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}

	got := c.ensureTurn("slack-owner-1700000000.000100")
	if got == nil || got.threadTS != "1700000000.000100" {
		t.Fatalf("thread not derived from the session key: %+v", got)
	}
}

// A session with no binding — a web-only conversation — must never be
// answered into a Slack channel.
func TestEnsureTurnRefusesWithoutBinding(t *testing.T) {
	sess := &sessionsWithBinding{has: false}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}
	if got := c.ensureTurn("ui-session-1"); got != nil {
		t.Fatalf("expected no turn for a session with no binding, got %+v", got)
	}
}

// Another instance's session must be left alone: replying as the wrong bot is
// worse than not replying.
func TestEnsureTurnRefusesForeignSession(t *testing.T) {
	sess := &sessionsWithBinding{
		binding: agentchannels.ThreadBinding{Channel: "slack", ChatID: "C123", ThreadID: "1.1"},
		has:     true,
	}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}
	if got := c.ensureTurn("slack-somebodyelse-1.1"); got != nil {
		t.Fatalf("expected no turn for another instance's session, got %+v", got)
	}
}

// An owned session with no binding is a fault, and it used to be a silent
// one: the turn ran, the answer was built, and NotifyState dropped it without
// a log. A schedule-driven session can lose a reply this way every day, so
// the drop has to leave a trace.
func TestEnsureTurnWarnsOnceForOwnedSessionWithoutBinding(t *testing.T) {
	sess := &sessionsWithBinding{has: false}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}

	if got := c.ensureTurn("slack-owner-1789014375.645429"); got != nil {
		t.Fatalf("expected no turn without a binding, got %+v", got)
	}
	if _, warned := c.unboundWarned.Load("slack-owner-1789014375.645429"); !warned {
		t.Fatal("an owned session with no binding should be reported")
	}

	// ensureTurn runs on every streamed delta — the report must not repeat.
	c.ensureTurn("slack-owner-1789014375.645429")
	n := 0
	c.unboundWarned.Range(func(any, any) bool { n++; return true })
	if n != 1 {
		t.Fatalf("expected one remembered session, got %d", n)
	}
}

// A session that is not ours is not a fault — NotifyState runs for every
// session on every channel, so warning here would fire on every web-only turn.
func TestEnsureTurnStaysQuietForForeignSession(t *testing.T) {
	sess := &sessionsWithBinding{has: false}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}

	c.ensureTurn("ui-session-1")
	if _, warned := c.unboundWarned.Load("ui-session-1"); warned {
		t.Error("a session this instance does not own must not be reported")
	}
}

// Once the binding turns up the session is healthy again, so the next failure
// is reported instead of being swallowed by the once-guard.
func TestEnsureTurnForgetsWarningAfterBindingAppears(t *testing.T) {
	sess := &sessionsWithBinding{has: false}
	c := &Channel{turns: map[string]*turn{}, sessions: sess, sessionPrefix: "slack-owner-"}
	c.ensureTurn("slack-owner-1789014375.645429")

	sess.has = true
	sess.binding = agentchannels.ThreadBinding{Channel: "slack", ChatID: "D0BACAJ7JRZ"}
	if got := c.ensureTurn("slack-owner-1789014375.645429"); got == nil {
		t.Fatal("turn should be restored once the binding exists")
	}
	if _, warned := c.unboundWarned.Load("slack-owner-1789014375.645429"); warned {
		t.Error("the warning should be forgotten once the session can be delivered to")
	}
}
