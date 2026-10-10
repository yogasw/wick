package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// replyPreviewMax caps TaskView.Reply: the thread previews a task, it
// does not replay it.
const replyPreviewMax = 1200

// capText cuts s to max runes, marking the cut.
func capText(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// userTaskLocked is the task sessionID sent, for a person acting on it from
// that conversation's UI; ErrUnknownTask for any other task, so a task id
// says nothing outside the conversation that sent it.
func (h *Hub) userTaskLocked(sessionID string, id a2a.TaskID) (*taskRef, error) {
	ref := h.tasks[id]
	if ref == nil {
		ref = h.loadLocked(id)
	}
	if ref == nil || sessionID == "" || ref.callerSession != sessionID {
		return nil, ErrUnknownTask
	}
	return ref, nil
}

// AnswerFromUser sends the user's answer to a question a teammate asked
// in a task sessionID sent (input_required): the answer goes to that same
// task, as the sending agent's message, and the sending agent is told in
// its conversation so it does not answer again. The first answer wins —
// the user's or the agent's — and any later one gets ErrAlreadyAnswered.
// The teammate's reply comes back into sessionID like any late reply.
// On the person's own task (taskRef.origin) the agent is not told and the
// reply only shows on the task: nothing wakes the agent's turn.
func (h *Hub) AnswerFromUser(ctx context.Context, sessionID, taskID, text string) (*Result, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	if text = strings.TrimSpace(text); text == "" {
		return nil, errors.New("answer is empty")
	}
	h.mu.Lock()
	ref, err := h.userTaskLocked(sessionID, id)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	if ref.claimed || ref.userAnswer || !ref.finished || ref.state != a2a.TaskStateInputRequired {
		waiting := ref.finished && ref.state == a2a.TaskStateInputRequired
		state := h.viewState(ref)
		claimed := ref.claimed || ref.userAnswer
		h.mu.Unlock()
		if claimed || waiting || state == "working" {
			// Someone's answer is already on its way or running.
			return nil, ErrAlreadyAnswered
		}
		return nil, fmt.Errorf("%w (it is %s)", ErrTaskNotWaiting, state)
	}
	caller, to, userTask := ref.callerAgentID, ref.to, ref.userTask()
	h.mu.Unlock()

	// Send claims the task (answering) in the same critical section as its
	// check, so an agent's answer racing this one gets ErrAlreadyAnswered —
	// or this one does. A failed send gives the question back ("Needs you").
	out, err := h.Send(ctx, SendInput{
		CallerSession: sessionID, CallerAgentID: caller, TaskID: string(id),
		Text: "Answer from the user:\n\n" + text, Wait: -1, byUser: true,
	})
	if err != nil {
		if errors.Is(err, ErrTaskNotWaiting) {
			// Lost the race to an answer that already ran its turn.
			return nil, ErrAlreadyAnswered
		}
		return nil, err
	}
	// The person's own task is none of the agent's business: telling it
	// would only wake its turn.
	if h.Notify != nil && !userTask {
		note := fmt.Sprintf("The user answered %s's question [task %s] directly:\n\n%s\n\n(Already sent to the teammate — do not answer it again.", to.Label(), id, text)
		if out.State == "working" {
			note += " Its reply comes back here.)"
		} else {
			// The teammate already settled: the reply came back to this
			// call, so it rides along instead of being lost.
			note += fmt.Sprintf(")\n\nReply from %s [task %s, %s]:\n\n%s", to.Label(), id, out.State, out.ReplyText+out.Reason)
		}
		_ = h.Notify.Deliver(context.WithoutCancel(ctx), sessionID, note) // best-effort: the answer itself went through
	}
	return out, nil
}

// CancelFromUser cancels a task sessionID sent, on behalf of the person
// in that conversation: CancelTask as the agent that sent it. The agent
// is told in its conversation, so it does not wait for a reply that will
// not come.
func (h *Hub) CancelFromUser(ctx context.Context, sessionID, taskID string) (*Result, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	h.mu.Lock()
	ref, err := h.userTaskLocked(sessionID, id)
	var caller string
	var to Peer
	userTask := false
	if err == nil {
		caller, to, userTask = ref.callerAgentID, ref.to, ref.userTask()
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out, err := h.cancelTask(ctx, caller, string(id), true)
	if err == nil && h.Notify != nil && !userTask {
		note := fmt.Sprintf("The user canceled the task to %s [task %s]. No reply will come back for it; do not resend it unless the user asks.", to.Label(), id)
		_ = h.Notify.Deliver(context.WithoutCancel(ctx), sessionID, note) // best-effort: the cancel itself went through
	}
	return out, err
}
