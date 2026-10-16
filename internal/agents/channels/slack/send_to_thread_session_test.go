package slack

import (
	"context"
	"strings"
	"sync"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// fixedSessions is a SessionChecker whose sessions either all exist or
// none do, recording the bindings written to it.
type fixedSessions struct {
	exists bool
	mu     sync.Mutex
	wrote  []agentchannels.ThreadBinding
}

func (f *fixedSessions) SessionExists(string) bool { return f.exists }
func (f *fixedSessions) AutoReplyOn(string) bool   { return false }
func (f *fixedSessions) SetAutoReply(string, bool) {}
func (f *fixedSessions) ThreadBinding(string) (agentchannels.ThreadBinding, bool) {
	return agentchannels.ThreadBinding{}, false
}
func (f *fixedSessions) SetThreadBinding(_ string, b agentchannels.ThreadBinding) {
	f.mu.Lock()
	f.wrote = append(f.wrote, b)
	f.mu.Unlock()
}

type sentMsg struct{ sessionID, role, text string }

func newSendToSessionChannel(t *testing.T, exists bool) (*Channel, *fixedSessions, *callLog, *[]sentMsg) {
	t.Helper()
	l, api := newCallLogSlack(t)
	sess := &fixedSessions{exists: exists}
	var mu sync.Mutex
	var sent []sentMsg
	c := &Channel{
		api: api, turns: map[string]*turn{}, sessions: sess,
		sessionPrefix: "slack-owner-", ownerUserID: "owner",
	}
	c.sendFn = func(_ context.Context, sessionID, _, _, role, text string) error {
		mu.Lock()
		sent = append(sent, sentMsg{sessionID, role, text})
		mu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		c.mu.Lock()
		for _, tr := range c.turns {
			c.stopStatusAnimation(tr)
		}
		c.mu.Unlock()
	})
	return c, sess, l, &sent
}

// require_existing on a thread whose session is gone refuses: no session is
// created, nothing is sent to the pool and nothing is posted to Slack.
func TestSendToThreadSessionRequireExistingRefusesMissing(t *testing.T) {
	c, sess, l, sent := newSendToSessionChannel(t, false)

	sid, isNew, err := c.SendToThreadSession(context.Background(), "C1", "1789014375.645429", "continue", false, true)
	if err == nil || !strings.Contains(err.Error(), "session slack-owner-1789014375.645429 not found") {
		t.Fatalf("want session not found error, got %v", err)
	}
	if sid != "slack-owner-1789014375.645429" || isNew {
		t.Fatalf("sid=%q isNew=%v", sid, isNew)
	}
	if len(*sent) != 0 {
		t.Fatalf("pool must not be called, got %v", *sent)
	}
	if len(sess.wrote) != 0 {
		t.Fatalf("no thread binding may be written, got %v", sess.wrote)
	}
	if len(c.turns) != 0 {
		t.Fatalf("no turn may be registered, got %v", c.turns)
	}
	l.mu.Lock()
	n := len(l.calls)
	l.mu.Unlock()
	if n != 0 {
		t.Fatalf("nothing may be posted to Slack, got %v", l.calls)
	}
}

// require_existing on a thread whose session exists continues it.
func TestSendToThreadSessionRequireExistingContinuesExisting(t *testing.T) {
	c, _, _, sent := newSendToSessionChannel(t, true)

	sid, isNew, err := c.SendToThreadSession(context.Background(), "C1", "1789014375.645429", "continue", false, true)
	if err != nil {
		t.Fatalf("SendToThreadSession: %v", err)
	}
	if sid != "slack-owner-1789014375.645429" || isNew {
		t.Fatalf("sid=%q isNew=%v", sid, isNew)
	}
	if len(*sent) != 1 || (*sent)[0].role != "user" || (*sent)[0].text != "continue" {
		t.Fatalf("want one user message sent, got %v", *sent)
	}
}

// Without require_existing a missing session is still started (first-check
// relies on this to open the card's session).
func TestSendToThreadSessionDefaultStartsMissing(t *testing.T) {
	c, _, _, sent := newSendToSessionChannel(t, false)

	_, isNew, err := c.SendToThreadSession(context.Background(), "C1", "1789014375.645429", "start", false, false)
	if err != nil {
		t.Fatalf("SendToThreadSession: %v", err)
	}
	if !isNew {
		t.Fatal("want new_session=true")
	}
	last := (*sent)[len(*sent)-1]
	if last.role != "user" || last.text != "start" {
		t.Fatalf("user message not sent, got %v", *sent)
	}
}
