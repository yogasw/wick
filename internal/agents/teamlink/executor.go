package teamlink

import (
	"context"
	"errors"
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

// Execute runs one turn of the agent for the message, in the chat chatFor
// picks: the one paired with the caller's conversation, or its main chat
// where Turns cannot pair.
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
			chatUser, _ := meta[metaChatUser].(string)
			sessionUser, _ := meta[metaSessionUser].(string)
			switch {
			case sessionUser != "" && sessionUser != from.OwnerID:
				// A person's mention in their chat with an agent shared
				// with them: re-checked against that person now, whatever
				// the metadata claims.
				shared, ok := h.forSessionUser(ctx, sessionUser, chatUser, from, target)
				if !ok {
					e.fail(ctx, ec, yield, ErrNotShared)
					return
				}
				target = shared
			case from.OwnerID != target.OwnerID:
				// Never across owners, whatever the metadata claims —
				// except an agent still shared with the sender's owner,
				// whose turn then runs in that owner's chat with it.
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
		resume, _ := meta[metaChat].(string)
		chat, err := h.chatFor(ctx, ec.ContextID, callerSession, target, newChat, resume)
		if err != nil {
			e.fail(ctx, ec, yield, err)
			return
		}
		// The sender's result names the chat (Result.Chat).
		h.mu.Lock()
		ref := h.tasks[ec.TaskID]
		if ref == nil {
			ref = &taskRef{touched: h.now()}
			h.tasks[ec.TaskID] = ref
		}
		ref.chat = chat
		h.mu.Unlock()
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
		canceled := ref.canceled
		h.mu.Unlock()
		var sessionID, reply string
		var runErr error
		// The task id rides on ctx, so Turns can tell this task's turn
		// from any other the session runs (TurnGate, TaskStopper).
		tctx := withTaskID(ctx, ec.TaskID)
		switch op, ok := h.Turns.(ChatOpener); {
		case canceled:
			// Canceled while the chat was being picked: no turn for it.
			runErr = ErrTaskCanceled
		case ok && chat != "":
			sessionID, reply, runErr = op.RunIn(tctx, target, chat, text)
		default:
			sessionID, reply, runErr = h.Turns.Run(tctx, target, text)
		}
		h.mu.Lock()
		delete(h.inflight[e.agentID], ec.TaskID)
		if len(h.inflight[e.agentID]) == 0 {
			delete(h.inflight, e.agentID)
		}
		h.last[e.agentID] = inbound{contextID: ec.ContextID, depth: depth, at: h.now()}
		answeredIn := sessionID
		if answeredIn == "" {
			answeredIn = session
		}
		// A canceled task answered nobody: later follow-ups of that
		// session stay with whatever it answered before.
		if ref := h.tasks[ec.TaskID]; answeredIn != "" && (ref == nil || !ref.canceled) {
			h.answered[answeredIn] = ec.TaskID
			if ref != nil {
				ref.answered = answeredIn
			}
		}
		h.mu.Unlock()

		state := a2a.TaskStateCompleted
		var out *a2a.Message
		reply = strings.TrimSpace(reply)
		var end *TurnEnd
		switch {
		case errors.As(runErr, &end):
			state, reply = end.State, strings.TrimSpace(end.Text)
			if reply == "" && state != a2a.TaskStateCompleted {
				reply = "the teammate ended the task as " + stateName(state)
			}
		case runErr != nil:
			state = a2a.TaskStateFailed
			reply = "the teammate's turn failed: " + runErr.Error()
		case reply == SilentToken:
			reply = ""
		case strings.HasPrefix(reply, InputRequiredToken):
			state = a2a.TaskStateInputRequired
			reply = strings.TrimSpace(strings.TrimPrefix(reply, InputRequiredToken))
		}
		if reply != "" {
			out = a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(reply))
		}
		e.audit(ctx, from, target, callerSession, sessionID, ec, state)
		yield(a2a.NewStatusUpdateEvent(ec, state, out), nil)
		h.finished(ctx, ec.TaskID, state, reply)
	}
}

// forSessionUser checks a mention user wrote in their chat with from: from
// must still be shared with user, and target must be user's own agent (run
// in user's chat, chatUser "") or still shared with user (chatUser = user).
// It returns target as the turn should see it.
func (h *Hub) forSessionUser(ctx context.Context, user, chatUser string, from, target Peer) (Peer, bool) {
	if _, ok := h.sharedByID(ctx, user, from.ID); !ok {
		return Peer{}, false
	}
	if target.OwnerID == user && chatUser == "" {
		return target, true
	}
	if chatUser != user {
		return Peer{}, false
	}
	return h.sharedByID(ctx, user, target.ID)
}

// fail ends a task the turn never started for. A refusal — the teammate
// is gone, no longer shared, or the chat asked for is not the caller's —
// ends it rejected; anything else failed. Either way err's text is the
// reason.
func (e *executor) fail(ctx context.Context, ec *a2asrv.ExecutorContext, yield func(a2a.Event, error) bool, err error) {
	state := a2a.TaskStateFailed
	for _, refusal := range []error{ErrUnknownHandle, ErrNotShared, ErrUnknownChat, ErrNewChatUnsupported, ErrMentionsOff} {
		if errors.Is(err, refusal) {
			state = a2a.TaskStateRejected
		}
	}
	msg := a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(err.Error()))
	yield(a2a.NewStatusUpdateEvent(ec, state, msg), nil)
	e.hub.finished(ctx, ec.TaskID, state, err.Error())
}

// audit writes mention_handoff into both threads: once as working when
// the task is picked up, once more with its final state.
func (e *executor) audit(ctx context.Context, from, to Peer, callerSession, targetSession string, ec *a2asrv.ExecutorContext, state a2a.TaskState) {
	if e.hub.Notify == nil {
		return
	}
	ev := Handoff{From: from.Handle, To: to.Handle, ToID: to.ID, ContextID: ec.ContextID, TaskID: string(ec.TaskID), State: state, FromSession: callerSession}
	// The Hub's task is the source of truth; the message's mark covers a
	// first turn that starts before Send registered the task. Either can
	// only make the task the person's, which delivers less, never more.
	e.hub.mu.Lock()
	if ref := e.hub.tasks[ec.TaskID]; ref != nil && ref.userTask() {
		ev.Origin = OriginUser
	}
	e.hub.mu.Unlock()
	if ec.Message != nil {
		if o, _ := ec.Message.Metadata[metaOrigin].(string); o == OriginUser {
			ev.Origin = OriginUser
		}
	}
	if ev.From == "" {
		ev.From = "user"
	}
	for _, s := range []string{callerSession, targetSession} {
		if s != "" {
			e.hub.Notify.Audit(context.WithoutCancel(ctx), s, ev)
		}
	}
}

// Cancel marks the task canceled, with the reason Hub.CancelTask noted.
// The turn itself is stopped by Hub.CancelTask (TaskStopper); whatever it
// reports afterwards is dropped.
func (e *executor) Cancel(_ context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		var msg *a2a.Message
		e.hub.mu.Lock()
		if ref := e.hub.tasks[ec.TaskID]; ref != nil && ref.canceled && ref.reply != "" {
			msg = a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewTextPart(ref.reply))
		}
		e.hub.mu.Unlock()
		yield(a2a.NewStatusUpdateEvent(ec, a2a.TaskStateCanceled, msg), nil)
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
