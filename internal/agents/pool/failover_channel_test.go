package pool

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/event"
)

// threadChannel buffers a turn's text like a channel thread does and keeps
// what it would post: the reply at Done and any notice.
type threadChannel struct {
	mu      sync.Mutex
	buf     strings.Builder
	replies []string
	notices []string
}

func (c *threadChannel) Name() string                { return "thread" }
func (c *threadChannel) Start(context.Context) error { return nil }
func (c *threadChannel) Stop()                       {}
func (c *threadChannel) IsConfigured() bool          { return true }
func (c *threadChannel) OnAgentEvent(_ string, ev event.AgentEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch ev.Type {
	case event.TextDelta:
		c.buf.WriteString(ev.Text)
	case event.Warning:
		if ev.FailedOver {
			c.buf.Reset()
		}
	case event.Notice:
		c.notices = append(c.notices, ev.Text)
	case event.Done, event.Error:
		c.replies = append(c.replies, c.buf.String()+ev.ErrorMsg)
		c.buf.Reset()
	}
}

func (c *threadChannel) seen() ([]string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.replies...), append([]string(nil), c.notices...)
}

// threadEnv is failoverEnv with a channel registry wired the way the server
// wires it: agent events and a failover's abort reach the thread.
func threadEnv(t *testing.T, turns map[string][][]string) (*Pool, *threadChannel) {
	t.Helper()
	p, _, _, _ := failoverEnv(t, true, turns, false)
	th := &threadChannel{}
	reg := channels.NewRegistry()
	reg.Add(th, nil)
	f := p.cfg.Factory.(*ClaudeFactory)
	prev := f.OnEvent
	f.OnEvent = func(sid, name string, ev event.AgentEvent) {
		prev(sid, name, ev)
		reg.DispatchAgentEventFrom(sid, name, ev)
	}
	p.cfg.OnFailoverAbort = func(sid, name string, evs []event.AgentEvent) {
		for _, ev := range evs {
			reg.DispatchAgentEventFrom(sid, name, ev)
		}
	}
	return p, th
}

// An automatic failover stays out of the thread: no notice and none of the
// failed provider's text (its partial answer, its limit line), only the
// next provider's answer.
func TestFailoverKeepsTheThreadQuiet(t *testing.T) {
	p, th := threadEnv(t, map[string][][]string{
		"a": {foLimitLines("a first half of the answer")},
		"b": {foOKLines("the answer on b")},
	})
	if err := p.Send(context.Background(), "S", "default", "slack", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { r, _ := th.seen(); return len(r) >= 1 }, 10*time.Second)
	time.Sleep(200 * time.Millisecond)
	replies, notices := th.seen()
	if len(notices) != 0 {
		t.Fatalf("the thread got notices %q", notices)
	}
	if len(replies) != 1 || strings.TrimSpace(replies[0]) != "the answer on b" {
		t.Fatalf("thread replies = %q, want only the answer on b", replies)
	}
}

// A switch typed in a channel ("#provider") still answers there.
func TestManualSwitchStillRepliesInTheThread(t *testing.T) {
	p, layout, _, _ := failoverEnv(t, true, nil, false)
	var replies []string
	send := channels.WrapSendFunc(func(context.Context, string, string, string, string, string) error {
		t.Fatal("a switch-only message was sent on")
		return nil
	}, layout, p, func(_, _, _, text string) { replies = append(replies, text) })
	if err := send(context.Background(), "S", "default", "slack", "user", "#claude/b"); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || !strings.HasPrefix(replies[0], "Switched to") {
		t.Fatalf("replies = %q, want the switch confirmation", replies)
	}
	if a := agentNow(t, layout); a.Provider != "claude/b" {
		t.Fatalf("provider = %q", a.Provider)
	}
}
