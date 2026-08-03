package api

import (
	"context"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	event "github.com/yogasw/wick/internal/agents/event"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/session"
)

// Async delivery + take-over wiring for sub-agent delegation.
//
// The delegation package deliberately knows nothing about channels or the
// pool; these adapters supply that knowledge so async results can reach a
// human and a human can reach a running sub-agent.

// poolDeliverer routes finished async results.
type poolDeliverer struct {
	pool     *agentpool.Pool
	channels *agentchannels.Registry
}

// DeliverToChannel posts a result back to the conversation that asked for
// it.
//
// Written as a system-sourced turn rather than a user message: the text
// is a report ABOUT the sub-agent's work, and injecting it as a user turn
// would make the leader treat wick's own status update as a human
// instruction.
func (d poolDeliverer) DeliverToChannel(ctx context.Context, parentSessionID, text string) error {
	if d.channels == nil {
		// No channel layer wired (headless/tests): fall back to the
		// session path so the result still reaches somewhere, rather than
		// being dropped after it was paid for.
		return d.DeliverToSession(ctx, parentSessionID, "", text)
	}
	// Reuse the normal agent-event path so the result renders and routes
	// exactly like any other agent output — one delivery mechanism, no
	// second formatting path that could drift from it. TextDelta then Done
	// is the shape every channel consumer already expects for a complete
	// turn.
	d.channels.DispatchAgentEvent(parentSessionID, event.AgentEvent{Type: event.TextDelta, Text: text})
	d.channels.DispatchAgentEvent(parentSessionID, event.AgentEvent{Type: event.Done})
	return nil
}

// DeliverToSession re-prompts the leader with the result, waking it so it
// can act on work it fired in the background.
func (d poolDeliverer) DeliverToSession(ctx context.Context, parentSessionID, agentName, text string) error {
	if d.pool == nil {
		return nil
	}
	// Source "subagent", not OriginUI. This text did not come from the
	// person at the keyboard, and labelling it as though it did tells the
	// leader its operator said something they never said — and hides from
	// the reader that a sub-agent came back at all.
	return d.pool.Send(ctx, parentSessionID, agentName, sourceSubAgent, "user", text)
}

// sourceSubAgent marks a turn wick posted on a sub-agent's behalf. The
// front-end badges any source that is not the local UI.
const sourceSubAgent = "subagent"

// poolSteerer delivers a human take-over message into a running
// sub-agent's session.
type poolSteerer struct {
	pool *agentpool.Pool
}

// SendToChild injects the human's message as a normal user turn, so the
// sub-agent reacts to it exactly as it would to any instruction — it
// queues behind the in-flight turn rather than interrupting it.
func (s poolSteerer) SendToChild(ctx context.Context, childSessionID, agentName, message string) error {
	if s.pool == nil {
		return nil
	}
	return s.pool.Send(ctx, childSessionID, agentName, string(session.OriginUI), "user", message)
}

// poolWaker makes a sub-agent readable-to again after its process has
// exited, so a message sent minutes later still reaches somebody.
type poolWaker struct {
	pool   *agentpool.Pool
	layout agentconfig.Layout
}

// WakeChild reports whether the target can receive a message with its
// memory intact.
//
// It does not spawn: pool.Send already spawns on demand, and the delivery
// that follows this call goes through exactly that path. What this adds is
// the HONESTY check — whether the spawn about to happen will resume the
// child's transcript or start a blank one. A resumed agent answers from
// what it did before; a blank one answers confidently from nothing, and
// the sender cannot tell the two apart without being told.
func (w poolWaker) WakeChild(_ context.Context, childSessionID, agentName string) error {
	if w.pool == nil {
		return nil
	}
	// Already running: its context is whatever it currently holds.
	for _, a := range w.pool.ActiveSnapshot() {
		if a.SessionID == childSessionID {
			return nil
		}
	}
	sess, err := session.Load(w.layout, childSessionID)
	if err != nil {
		return delegation.ErrContextLost
	}
	for _, a := range sess.Agents {
		if agentName != "" && a.Name != agentName {
			continue
		}
		if a.CLISessionID != "" {
			return nil // resumable — the next Send will pass --resume
		}
	}
	return delegation.ErrContextLost
}
