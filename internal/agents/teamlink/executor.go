package teamlink

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// SilentToken is the reply that means "nothing to add". The task still
// completes, just without text, so no acknowledgement ping-pongs back.
const SilentToken = "[silent]"

// executor is the a2asrv.AgentExecutor of one Team agent.
type executor struct {
	hub     *Hub
	agentID string
}

// handler returns agentID's request handler, creating it on first use.
// The task store is a genStore, so settled tasks age out (see store.go).
func (h *Hub) handler(agentID string) a2asrv.RequestHandler {
	h.mu.Lock()
	defer h.mu.Unlock()
	if rh, ok := h.handlers[agentID]; ok {
		return rh
	}
	st := newGenStore(h.now())
	rh := a2asrv.NewHandler(&executor{hub: h, agentID: agentID}, a2asrv.WithTaskStore(st))
	h.handlers[agentID], h.stores[agentID] = rh, st
	return rh
}

// Frame is how an inbound message reads to the target: who sent it,
// stated by wick, then the text. It is a peer's input, not the user's.
func Frame(from Peer, text string) string {
	return fmt.Sprintf("Message from %s:\n%s", from.Label(), text)
}

// Execute runs one turn of the agent's main conversation for the message —
// or of the chat a new_chat opened for its exchange.
func (e *executor) Execute(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if ec.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(ec, ec.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateWorking, nil), nil) {
			return
		}
		h := e.hub
		target, err := h.Dir.Get(ctx, e.agentID)
		if err == nil && target.Disabled {
			err = ErrUnknownHandle
		}
		if err != nil {
			e.fail(ctx, ec, yield, err)
			return
		}
		meta := ec.Message.Metadata
		fromID, _ := meta[metaFrom].(string)
		callerSession, _ := meta[metaSession].(string)
		depth := metaInt(meta[metaDepth])
		text := messageText(ec.Message)
		var from Peer
		if fromID != "" {
			if from, err = h.Dir.Get(ctx, fromID); err != nil {
				e.fail(ctx, ec, yield, ErrUnknownHandle)
				return
			}
			if from.OwnerID != target.OwnerID {
				// Never across owners, whatever the metadata claims —
				// except an agent still shared with the sender's owner,
				// whose turn then runs in that owner's chat with it.
				chatUser, _ := meta[metaChatUser].(string)
				shared, ok := h.sharedByID(ctx, from.OwnerID, target.ID)
				if chatUser != from.OwnerID || !ok {
					e.fail(ctx, ec, yield, ErrNotShared)
					return
				}
				target = shared
			}
			// A remote agent gets the mention text alone: the frame names
			// a local agent, which is not the remote's business.
			if !target.Remote {
				text = Frame(from, text)
			}
		}

		newChat, _ := meta[metaNewChat].(bool)
		chat, err := h.chatFor(ctx, ec.ContextID, target, newChat)
		if err != nil {
			e.fail(ctx, ec, yield, err)
			return
		}
		session := chat
		if sl, ok := h.Turns.(SessionLocator); ok && session == "" {
			session = sl.MainSession(ctx, target)
		}
		// The handoff row appears as soon as the task is picked up and is
		// moved to its final state below (same task id, so one row).
		e.audit(ctx, from, target, callerSession, session, ec, a2a.TaskStateWorking)
		h.mu.Lock()
		if h.inflight[e.agentID] == nil {
			h.inflight[e.agentID] = map[a2a.TaskID]inbound{}
		}
		h.inflight[e.agentID][ec.TaskID] = inbound{contextID: ec.ContextID, depth: depth, session: session}
		h.mu.Unlock()
		var sessionID, reply string
		var runErr error
		if op, ok := h.Turns.(ChatOpener); ok && chat != "" {
			sessionID, reply, runErr = op.RunIn(ctx, target, chat, text)
		} else {
			sessionID, reply, runErr = h.Turns.Run(ctx, target, text)
		}
		h.mu.Lock()
		delete(h.inflight[e.agentID], ec.TaskID)
		if len(h.inflight[e.agentID]) == 0 {
			delete(h.inflight, e.agentID)
		}
		h.last[e.agentID] = inbound{contextID: ec.ContextID, depth: depth, at: h.now()}
		if sessionID != "" {
			h.answered[sessionID] = ec.TaskID
		} else if session != "" {
			h.answered[session] = ec.TaskID
		}
		h.mu.Unlock()

		state := a2a.TaskStateCompleted
		var out *a2a.Message
		reply = strings.TrimSpace(reply)
		switch {
		case runErr != nil:
			state = a2a.TaskStateFailed
			reply = runErr.Error()
			out = a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(reply))
		case reply == SilentToken:
			reply = ""
		case reply != "":
			out = a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(reply))
		}
		e.audit(ctx, from, target, callerSession, sessionID, ec, state)
		yield(a2a.NewStatusUpdateEvent(ec, state, out), nil)
		h.finished(ctx, ec.TaskID, state, reply)
	}
}

// fail ends the task as failed with err's text.
func (e *executor) fail(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool, err error) {
	msg := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(err.Error()))
	yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateFailed, msg), nil)
	e.hub.finished(ctx, ec.TaskID, a2a.TaskStateFailed, err.Error())
}

// audit writes mention_handoff into both threads: once as working when
// the task is picked up, once more with its final state.
func (e *executor) audit(ctx context.Context, from, to Peer, callerSession, targetSession string, ec *a2asrv.ExecutorContext, state a2a.TaskState) {
	if e.hub.Notify == nil {
		return
	}
	ev := Handoff{From: from.Handle, To: to.Handle, ToID: to.ID, ContextID: ec.ContextID, TaskID: string(ec.TaskID), State: state, FromSession: callerSession}
	if ev.From == "" {
		ev.From = "user"
	}
	for _, s := range []string{callerSession, targetSession} {
		if s != "" {
			e.hub.Notify.Audit(context.WithoutCancel(ctx), s, ev)
		}
	}
}

// Cancel marks the task canceled. The turn already queued in the pool is
// not pulled back; it simply has nobody waiting on it.
func (e *executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, nil), nil)
	}
}

// metaInt reads a depth that may have round-tripped through JSON.
func metaInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}
