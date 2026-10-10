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
		Text: "Answer from the user:\n\n" + text, Wait: -1, byUser: true, logText: text,
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

// MaxUserMessage caps a message the person writes on a task, in runes:
// the thread keeps an entry whole up to threadTextMax, so what they sent
// never reads cut right after sending.
const MaxUserMessage = threadTextMax

var (
	// ErrNoTaskChat refuses a message to a task whose teammate has no chat
	// picked yet: the executor has not taken the task up, so wick does not
	// know which chat it will run in.
	ErrNoTaskChat = errors.New("the teammate has not picked this task up yet, so there is no chat to send to — try again once it is working")
	// ErrTaskMoved refuses a message to a task a restart replaced, when the
	// task that took it over cannot be found.
	ErrTaskMoved = errors.New("this task was replaced after a restart and its replacement is gone")
	// ErrMessageTooLong refuses a message over MaxUserMessage.
	ErrMessageTooLong = fmt.Errorf("message is longer than %d characters", MaxUserMessage)
)

// supersedeHops bounds following supersededBy: a chain longer than a
// couple of restarts is not one wick makes.
const supersedeHops = 4

// liveTaskLocked is the task sessionID sent that a message on id lands
// on: id itself, or the task that took over its thread when a restart
// lost it (supersede). ErrUnknownTask as userTaskLocked; ErrTaskMoved
// when the replacement is not known.
func (h *Hub) liveTaskLocked(sessionID string, id a2a.TaskID) (a2a.TaskID, *taskRef, error) {
	ref, err := h.userTaskLocked(sessionID, id)
	for hop := 0; err == nil && hop < supersedeHops; hop++ {
		next := ref.supersededBy
		if next == "" {
			// A record from before supersededBy was kept says so only in
			// its reason.
			if ref.canceled && strings.HasPrefix(ref.reply, "superseded by task ") {
				return "", nil, ErrTaskMoved
			}
			return id, ref, nil
		}
		if ref, err = h.userTaskLocked(sessionID, next); err != nil {
			return "", nil, ErrTaskMoved
		}
		id = next
	}
	if err != nil {
		return "", nil, err
	}
	return "", nil, ErrTaskMoved
}

// userMessageText frames the person's message for the teammate's chat:
// a header saying who wrote it and on which task, then their words
// quoted line by line, so nothing they typed reads as a frame wick posts.
func userMessageText(id a2a.TaskID, title, text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return fmt.Sprintf("Message from the user [task %s · %s] (they wrote it in the task's thread):\n\n%s", id, title, strings.Join(lines, "\n"))
}

// MessageFromUser sends the person's message on a task sessionID sent,
// from its thread ("Reply in thread"). A question waiting for them
// (input_required) takes it as their answer (AnswerFromUser). Any other
// task — working, or ended — gets it as a user entry on its thread, and
// the teammate gets it in the chat the task runs in. The task keeps its
// state, as a FollowUp on an ended task does: the message joins the task,
// it does not reopen it, and nothing comes back on the sender's side. A
// task a restart replaced passes the message on to its replacement.
func (h *Hub) MessageFromUser(ctx context.Context, sessionID, taskID, text string) (*Result, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	if text = strings.TrimSpace(text); text == "" {
		return nil, errors.New("message is empty")
	}
	if len([]rune(text)) > MaxUserMessage {
		return nil, ErrMessageTooLong
	}
	// The main chat is read off the pool, never under h.mu: look it up
	// first, for the case the decision below needs it.
	h.mu.Lock()
	_, ref, err := h.liveTaskLocked(sessionID, id)
	var to Peer
	if err == nil {
		to = ref.to
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	main := ""
	if sl, ok := h.Turns.(SessionLocator); ok {
		main = sl.MainSession(ctx, to)
	}

	// One critical section decides and records: a question the teammate
	// raised meanwhile takes the message as the answer, and the entry is
	// on the thread before it goes out, so the task cannot vanish between
	// delivering it and logging it.
	h.mu.Lock()
	live, ref, err := h.liveTaskLocked(sessionID, id)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	if ref.finished && ref.state == a2a.TaskStateInputRequired && !ref.claimed && !ref.userAnswer {
		h.mu.Unlock()
		return h.AnswerFromUser(ctx, sessionID, string(live), text)
	}
	chat := ref.chat
	switch {
	case chat != "":
	case ref.answered != "":
		chat = ref.answered
	case ref.chatPicked:
		// The executor picked the target's main chat ("" once picked):
		// that is where the task runs, so that is where the message goes.
		chat = main
	}
	if chat == "" || h.Notify == nil {
		h.mu.Unlock()
		return nil, ErrNoTaskChat
	}
	to = ref.to
	entry := ThreadEntry{Role: ThreadUser, Text: text, At: h.now()}
	ref.logLocked(entry)
	ref.touched = entry.At
	out := h.resultLocked(live, ref)
	msg := userMessageText(live, ref.title, text)
	h.mu.Unlock()

	// It is the person's message: a cancel of the task does not stop the
	// turn it wakes (WithPersonMessage).
	if err := h.Notify.Deliver(context.WithoutCancel(WithPersonMessage(ctx)), chat, msg); err != nil {
		h.mu.Lock()
		if ref := h.tasks[live]; ref != nil {
			ref.unlogLocked(entry)
		}
		h.mu.Unlock()
		h.persist(live)
		return nil, fmt.Errorf("team: message @%s: %w", to.Handle, err)
	}
	h.persist(live)
	return out, nil
}
