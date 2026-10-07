package a2aserver

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/remote"
)

// HealthKey is the metadata flag of the connection test's message: the
// executor answers it with "pong" without waking the agent, so a test
// proves auth, routing and the executor but spends no turn.
const HealthKey = "wick_health"

// executor bridges one agent's A2A tasks to the wick pool.
type executor struct {
	s       *Server
	agentID string
}

var _ a2asrv.AgentExecutor = (*executor)(nil)

// Execute sends the message as a turn of the agent and translates the
// turn's events: text deltas become one appended artifact, Done completes
// the task with the full reply, Error fails it.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		fail := func(msg string) {
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed, agentMessage(ec, msg)), nil)
		}
		a, ok := e.s.dir.Agent(ctx, e.agentID)
		if !ok || a.Disabled {
			fail("agent unavailable")
			return
		}
		if isHealth(ec) {
			yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCompleted, agentMessage(ec, "pong")), nil)
			return
		}
		text := messageText(ec.Message)
		if text == "" {
			fail("empty message: send at least one text part")
			return
		}
		hops := inboundHops(ec)
		if hops > remote.MaxHops {
			fail(fmt.Sprintf("refused: this request already passed through %d wick A2A hops (limit %d); a remote agent probably points back at wick", hops, remote.MaxHops))
			return
		}
		sid := SessionID(a.ID, ec.ContextID)
		// A remote agent behind this one sends hops+1 on its own call.
		remote.SetHops(sid, hops)
		tn, err := e.s.dispatch(a, sid, callerOf(ec.User), ec.ContextID, text)
		if err != nil {
			fail(err.Error())
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}
		tr := translator{ec: ec}
		for {
			select {
			case <-tn.wake:
			case <-ctx.Done():
				// The caller left. The turn stays queued so its terminal
				// event still pops it and later replies stay aligned.
				return
			}
			for _, ev := range tn.drain() {
				out, last := tr.next(ev)
				if out != nil && !yield(out, nil) {
					return
				}
				if last {
					return
				}
			}
		}
	}
}

// Cancel marks the task canceled. The agent's turn is not killed: it lives
// in a session the owner may be watching, and stopping it is theirs to do.
func (e *executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

// translator turns one turn's wick events into A2A events.
type translator struct {
	ec       a2a.TaskInfoProvider
	artifact a2a.ArtifactID
	full     strings.Builder
}

// next maps ev; last reports a terminal event.
func (t *translator) next(ev event.AgentEvent) (out a2a.Event, last bool) {
	switch ev.Type {
	case event.TextReplace:
		// The artifact streamed so far cannot be taken back; the final
		// message carries the whole reply.
		t.full.Reset()
		t.full.WriteString(ev.Text)
		return nil, false
	case event.TextDelta:
		if ev.Text == "" {
			return nil, false
		}
		t.full.WriteString(ev.Text)
		if t.artifact == "" {
			e := a2a.NewArtifactEvent(t.ec, a2a.NewTextPart(ev.Text))
			t.artifact = e.Artifact.ID
			return e, false
		}
		return a2a.NewArtifactUpdateEvent(t.ec, t.artifact, a2a.NewTextPart(ev.Text)), false
	case event.Done:
		var msg *a2a.Message
		if reply := strings.TrimSpace(t.full.String()); reply != "" {
			msg = agentMessage(t.ec, reply)
		}
		return a2a.NewStatusUpdateEvent(t.ec, a2a.TaskStateCompleted, msg), true
	case event.Error:
		msg := strings.TrimSpace(ev.ErrorMsg)
		if msg == "" {
			msg = strings.TrimSpace(ev.Text)
		}
		if msg == "" {
			msg = "agent turn failed"
		}
		return a2a.NewStatusUpdateEvent(t.ec, a2a.TaskStateFailed, agentMessage(t.ec, msg)), true
	}
	return nil, false
}

func agentMessage(ti a2a.TaskInfoProvider, text string) *a2a.Message {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ti, a2a.NewTextPart(text))
}

// messageText flattens a message into the prompt: text parts as they are,
// anything else as JSON, so structured input still reaches the agent.
func messageText(m *a2a.Message) string {
	if m == nil {
		return ""
	}
	var parts []string
	for _, p := range m.Parts {
		if p == nil {
			continue
		}
		if t := p.Text(); t != "" {
			parts = append(parts, t)
			continue
		}
		if b, err := json.Marshal(p); err == nil && string(b) != "{}" {
			parts = append(parts, string(b))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// inboundHops is the request's remote.HopsKey: set by a wick caller,
// absent (0) from anyone else.
func inboundHops(ec *a2asrv.ExecutorContext) int {
	if ec.Message != nil {
		if n := remote.HopsOf(ec.Message.Metadata[remote.HopsKey]); n > 0 {
			return n
		}
	}
	return remote.HopsOf(ec.Metadata[remote.HopsKey])
}

func isHealth(ec *a2asrv.ExecutorContext) bool {
	if v, _ := ec.Metadata[HealthKey].(bool); v {
		return true
	}
	if ec.Message != nil {
		if v, _ := ec.Message.Metadata[HealthKey].(bool); v {
			return true
		}
	}
	return false
}
