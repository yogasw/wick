package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// A revision of a task (amend_task) changes or adds to what the task
// asks without making a second task: the teammate gets one job, and the
// asker one reply.
//
//   - Not started yet (waiting for its turn): the revision is kept on the
//     task and read with the brief when the turn starts (TurnText).
//   - Running: the revision goes into the session the turn runs in, as a
//     message of the same task.
//   - Asking (input_required): the revision is the answer.
//   - Ended: it goes as a new task in the same exchange, linked to the
//     old one (amendOf).

// ErrAmendEmpty refuses a revision without text.
var ErrAmendEmpty = errors.New("the revision is empty — say what changes")

// revision is one amend of a task that had not started its turn yet.
type revision struct {
	text    string
	at      time.Time
	replace bool
}

// AmendInput is a revision of a task the caller sent.
type AmendInput struct {
	// CallerSession and CallerAgentID are resolved from the session, as
	// for Send.
	CallerSession string
	CallerAgentID string
	TaskID        string
	Text          string
	// Replace swaps the brief for Text instead of adding to it.
	Replace bool
}

type turnTextKey struct{}

// withTurnText tags ctx with what the task's turn reads once it starts
// (TurnText).
func withTurnText(ctx context.Context, f func(string) string) context.Context {
	return context.WithValue(ctx, turnTextKey{}, f)
}

// TurnText is text with the revisions its task got while it waited for
// its turn. Turns calls it once the task owns the session's turn (after
// TurnGate.Acquire) and sends what it returns; from then on a revision
// goes into the running turn instead. Text alone outside a team task.
func TurnText(ctx context.Context, text string) string {
	if f, ok := ctx.Value(turnTextKey{}).(func(string) string); ok && f != nil {
		return f(text)
	}
	return text
}

// takeTurnText marks task id's turn as started and returns text with its
// waiting revisions.
func (h *Hub) takeTurnText(id a2a.TaskID, text string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ref := h.tasks[id]
	if ref == nil {
		return text
	}
	ref.turnTaken = true
	return withRevisions(text, ref.revisions)
}

// withRevisions appends revs to the task's first message; a replacing
// one says the brief above no longer stands.
func withRevisions(text string, revs []revision) string {
	if len(revs) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	for i, r := range revs {
		head := fmt.Sprintf("Revision %d (%s)", i+1, r.at.Format("15:04"))
		if r.replace {
			head += ", replacing everything above: follow this instead"
		}
		fmt.Fprintf(&b, "\n\n%s:\n%s", head, r.text)
	}
	return b.String()
}

// revisionText is a revision handed into a running turn.
func revisionText(from Peer, id a2a.TaskID, n int, text string, replace bool) string {
	head := fmt.Sprintf("Revision %d of task %s from %s", n, id, from.Label())
	if replace {
		head += ", replacing the brief: drop what it asked and do this instead"
	}
	return head + ":\n\n" + text + "\n\n(Same task: your final answer is still its one reply.)"
}

// Amend revises task in.TaskID (see above). The result is the task the
// revision went to: the same one, or the new one when it had ended.
func (h *Hub) Amend(ctx context.Context, in AmendInput) (*Result, error) {
	if strings.TrimSpace(in.Text) == "" {
		return nil, ErrAmendEmpty
	}
	caller, err := h.Dir.Get(ctx, in.CallerAgentID)
	if in.CallerAgentID == "" || err != nil {
		return nil, ErrNotTeamSession
	}
	id := a2a.TaskID(strings.TrimSpace(in.TaskID))
	h.mu.Lock()
	ref := h.tasks[id]
	if ref == nil {
		ref = h.loadLocked(id)
	}
	if ref == nil || ref.callerAgentID != caller.ID {
		err := h.unknownLocked(caller.ID, id)
		h.mu.Unlock()
		return nil, err
	}
	if ref.userTask() {
		h.mu.Unlock()
		return nil, ErrUserTask
	}
	switch {
	case ref.canceled:
		h.mu.Unlock()
		return nil, fmt.Errorf("%w (it was canceled) — send a new message instead", ErrTaskSettled)
	case ref.claimed:
		h.mu.Unlock()
		return nil, ErrAlreadyAnswered
	case ref.finished && ref.state == a2a.TaskStateInputRequired:
		// The task asks something: the revision answers it.
		h.mu.Unlock()
		return h.Send(ctx, SendInput{CallerSession: in.CallerSession, CallerAgentID: caller.ID, TaskID: string(id), Text: in.Text})
	case ref.finished:
		to, contextID := ref.to.Handle, ref.contextID
		h.mu.Unlock()
		return h.amendEnded(ctx, caller, in, id, to, contextID)
	}
	now := h.now()
	n := len(ref.revisions) + 1
	ref.revisions = append(ref.revisions, revision{text: in.Text, at: now, replace: in.Replace})
	ref.touched = now
	ref.logLocked(ThreadEntry{Role: ThreadCaptain, Handle: caller.Handle, Name: caller.Name, Text: fmt.Sprintf("↻ revision %d: %s", n, in.Text), At: now})
	running, isRunning := h.inflight[ref.agentID][id]
	started, handle := ref.turnTaken, ref.to.Handle
	h.mu.Unlock()
	h.persist(id)
	note := fmt.Sprintf("Revision %d added to task %s before its turn started: @%s reads it with the brief. Wait for the task's one reply.", n, id, handle)
	if started && isRunning && running.session != "" && h.Notify != nil {
		if err := h.Notify.Deliver(ctx, running.session, revisionText(caller, id, n, in.Text, in.Replace)); err != nil {
			return nil, fmt.Errorf("team: revise task %s: %w", id, err)
		}
		note = fmt.Sprintf("Revision %d went into @%s's running turn for task %s. Wait for the task's one reply.", n, handle, id)
	}
	res, err := h.GetTask(ctx, caller.ID, string(id))
	if res != nil {
		res.Note = note
	}
	return res, err
}

// amendEnded sends a revision of a task that already ended as a new task
// in the same exchange, linked both ways.
func (h *Hub) amendEnded(ctx context.Context, caller Peer, in AmendInput, id a2a.TaskID, to, contextID string) (*Result, error) {
	text := fmt.Sprintf("Revision of task %s (it already ended):\n\n%s", id, in.Text)
	res, err := h.Send(ctx, SendInput{CallerSession: in.CallerSession, CallerAgentID: caller.ID, To: to, Text: text, ContextID: contextID, Group: GroupNone})
	if err != nil || res == nil {
		return res, err
	}
	h.mu.Lock()
	if old := h.tasks[id]; old != nil {
		old.amendedBy = a2a.TaskID(res.TaskID)
	}
	if nw := h.tasks[a2a.TaskID(res.TaskID)]; nw != nil {
		nw.amendOf = id
	}
	h.mu.Unlock()
	h.persist(id)
	h.persist(a2a.TaskID(res.TaskID))
	res.Note = fmt.Sprintf("Task %s had already ended, so the revision went as new task %s in the same exchange. ", id, res.TaskID) + res.Note
	return res, nil
}
