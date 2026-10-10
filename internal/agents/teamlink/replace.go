package teamlink

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// How a task was sent again from the UI (taskRef.replacedHow).
const (
	// ReplaceRetry sends a failed task again to the same teammate.
	ReplaceRetry = "retry"
	// ReplaceReassign sends a declined or failed task to another teammate.
	ReplaceReassign = "reassign"
)

var (
	// ErrTaskNotRetryable refuses a Retry of a task that did not fail.
	ErrTaskNotRetryable = errors.New("only a failed task can be retried")
	// ErrTaskNotReassignable refuses a Reassign of a task that was neither
	// declined nor failed.
	ErrTaskNotReassignable = errors.New("only a declined or failed task can be reassigned")
	// ErrAlreadyReplaced refuses a second Retry or Reassign of one task:
	// it was already sent again (or that is on its way), so a double click
	// never makes two tasks.
	ErrAlreadyReplaced = errors.New("this task was already retried or reassigned")
	// ErrReassignSame refuses a Reassign to the teammate the task is with.
	ErrReassignSame = errors.New("the task is already with that teammate — pick another one")
	// ErrUnknownTeammate refuses a Reassign to a handle this conversation
	// does not reach.
	ErrUnknownTeammate = errors.New("no such teammate in this conversation")
	// ErrTeammateRefuses refuses a Reassign of the agent's task to a
	// teammate whose mention policy does not take tasks from that agent.
	ErrTeammateRefuses = errors.New("that teammate does not take tasks from this agent — pick another one")
	// ErrTeammateGone refuses a Retry when the task's teammate is no
	// longer one this conversation reaches (disabled, unshared, deleted).
	ErrTeammateGone = errors.New("the teammate this task went to is no longer reachable from this chat — use Reassign")
	// ErrTeammateChanged refuses a Retry when the task's handle now names
	// another agent than the one the task went to: the brief would reach
	// someone else.
	ErrTeammateChanged = errors.New("this handle now belongs to a different agent than the one the task went to — use Reassign")
)

// briefLocked is what a Retry or Reassign sends again: the task's first
// message as sent, or — on a record from before it was kept — the first
// entry of its thread when that entry is the brief (sent by the agent or
// the person when the task started; the thread keeps only its newest 50,
// so a long thread may have lost it), else the title.
func (r *taskRef) briefLocked() string {
	if strings.TrimSpace(r.brief) != "" {
		return r.brief
	}
	if len(r.log) > 0 {
		if e := r.log[0]; (e.Role == ThreadCaptain || e.Role == ThreadUser) && !e.At.After(r.started.Add(time.Second)) {
			return e.Text
		}
	}
	return r.title
}

// ReassignTarget is a teammate a task can be reassigned to, as the
// Reassign menu lists it.
type ReassignTarget struct {
	Handle string `json:"handle"`
	Name   string `json:"name"`
	// Shared: another owner shares the agent with this chat's user.
	Shared bool `json:"shared,omitempty"`
	// Refused: the agent's mention policy does not take the task's sender
	// (only for the agent's own task; the person's mention goes through).
	Refused bool `json:"refused,omitempty"`
}

// rosterFor is who a task's sender reaches: the person's own task what
// their @mention reaches, the agent's what the agent reaches — the lists
// Send resolves against, so a handle names the same agent here and there.
func (h *Hub) rosterFor(ctx context.Context, caller string, userTask bool, originUser string) ([]Peer, error) {
	if userTask {
		return h.MentionTargets(ctx, caller, originUser)
	}
	return h.Reachable(ctx, caller)
}

// ReassignTargets lists the teammates the Reassign of task taskID (one
// sessionID sent) may go to: the sender's roster without the task's own
// teammate, each marked when its mention policy refuses the agent.
func (h *Hub) ReassignTargets(ctx context.Context, sessionID, taskID string) ([]ReassignTarget, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	h.mu.Lock()
	ref, err := h.userTaskLocked(sessionID, id)
	var caller, originUser string
	var from Peer
	userTask := false
	if err == nil {
		caller, from, userTask, originUser = ref.callerAgentID, ref.to, ref.userTask(), ref.originUser
	}
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	peers, err := h.rosterFor(ctx, caller, userTask, originUser)
	if err != nil {
		return nil, err
	}
	var self Peer
	if !userTask {
		self, _ = h.Dir.Get(ctx, caller)
	}
	out := make([]ReassignTarget, 0, len(peers))
	for _, p := range peers {
		if p.ID == from.ID {
			continue
		}
		out = append(out, ReassignTarget{Handle: p.Handle, Name: p.Name, Shared: p.ChatUser != "", Refused: !userTask && !p.AcceptsFrom(self)})
	}
	return out, nil
}

// RetryFromUser sends a failed task sessionID sent again, as a new task to
// the same teammate with the same brief, on behalf of the person in that
// conversation. The old task stays as it ended and links to the new one.
func (h *Hub) RetryFromUser(ctx context.Context, sessionID, taskID string) (*Result, error) {
	return h.replaceFromUser(ctx, sessionID, taskID, "", ReplaceRetry)
}

// ReassignFromUser sends a declined or failed task sessionID sent to
// another teammate, as a new task with the same brief. to must be one of
// the teammates the conversation reaches, not the task's own.
func (h *Hub) ReassignFromUser(ctx context.Context, sessionID, taskID, to string) (*Result, error) {
	if to = NormalizeHandle(to); to == "" {
		return nil, errors.New("a teammate to reassign to is required")
	}
	return h.replaceFromUser(ctx, sessionID, taskID, to, ReplaceReassign)
}

// settlePendingReplace resolves a claim a restart left on task id
// (replacePending, nobody replacing it in this process): when the task it
// may have sent is in sessionID's list (retryOf), the link is written and
// ErrAlreadyReplaced returned; otherwise the claim is dropped and the
// Retry goes ahead.
func (h *Hub) settlePendingReplace(sessionID string, id a2a.TaskID) error {
	h.mu.Lock()
	ref := h.tasks[id]
	pending := ref != nil && ref.replacePending != "" && !ref.replacing && ref.replacedBy == ""
	h.mu.Unlock()
	if !pending {
		return nil
	}
	var sent *TaskView
	for _, v := range h.SentFrom(sessionID) {
		if v.RetryOf == string(id) {
			sent = &v
			break
		}
	}
	h.mu.Lock()
	if ref = h.tasks[id]; ref == nil || ref.replacing || ref.replacedBy != "" {
		h.mu.Unlock()
		return nil
	}
	if sent != nil {
		ref.replacedBy, ref.replacedHow, ref.replacedHandle, ref.replacedTo = a2a.TaskID(sent.TaskID), ref.replacePending, sent.ToHandle, sent.ToName
	}
	ref.replacePending = ""
	h.mu.Unlock()
	h.persist(id)
	if sent != nil {
		return ErrAlreadyReplaced
	}
	return nil
}

// replaceFromUser is Retry (to "") and Reassign. The task is claimed
// (replacing) under the same lock as its check, so of two clicks exactly
// one sends; the claim is on disk (replacePending) before anything is
// sent and the new task carries retryOf from its first record, so a
// restart in between never lets a second Retry through. The new task goes
// out as the old one did — the agent's task as that agent, the person's
// own (@mention) task as that person — to the same agent (by id, not just
// handle), so it reaches no one the old one could not, and its changes
// reach the sending chat like any task's.
//
// The new task starts a fresh exchange (no ContextID), so the hop budget
// starts over: on purpose, since a person asked for it — a hop limit
// bounds agents talking among themselves, not the person's own clicks.
func (h *Hub) replaceFromUser(ctx context.Context, sessionID, taskID, to, how string) (*Result, error) {
	id := a2a.TaskID(strings.TrimSpace(taskID))
	h.mu.Lock()
	_, err := h.userTaskLocked(sessionID, id)
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := h.settlePendingReplace(sessionID, id); err != nil {
		return nil, err
	}
	h.mu.Lock()
	ref, err := h.userTaskLocked(sessionID, id)
	if err == nil {
		state := h.viewState(ref)
		switch {
		case ref.replacedBy != "" || ref.replacing || ref.replacePending != "":
			err = ErrAlreadyReplaced
		case how == ReplaceRetry && state != "failed":
			err = fmt.Errorf("%w (it is %s)", ErrTaskNotRetryable, state)
		case how == ReplaceReassign && state != "rejected" && state != "failed":
			err = fmt.Errorf("%w (it is %s)", ErrTaskNotReassignable, state)
		case how == ReplaceReassign && NormalizeHandle(ref.to.Handle) == to:
			err = ErrReassignSame
		}
	}
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	caller, from, userTask, originUser := ref.callerAgentID, ref.to, ref.userTask(), ref.originUser
	brief := ref.briefLocked()
	ref.replacing, ref.replacePending = true, how
	h.mu.Unlock()
	h.persist(id)
	release := func() {
		h.mu.Lock()
		if r := h.tasks[id]; r != nil {
			r.replacing, r.replacePending = false, ""
		}
		h.mu.Unlock()
		h.persist(id)
	}

	target, err := h.replaceTarget(ctx, caller, from, userTask, originUser, to, how)
	if err != nil {
		release()
		return nil, err
	}
	out, err := h.Send(ctx, SendInput{
		CallerSession: sessionID, CallerAgentID: caller, SessionUser: originUser,
		To: target.Handle, Text: brief, Wait: -1, Human: userTask, retryOf: id,
	})
	if err != nil {
		release()
		return nil, err
	}
	next := a2a.TaskID(out.TaskID)
	h.mu.Lock()
	// The link names whom the new task really went to.
	if nr := h.tasks[next]; nr != nil {
		nr.retryOf = id
		if nr.to.ID != "" {
			target = nr.to
		}
	}
	if r := h.tasks[id]; r != nil {
		r.replacing, r.replacePending = false, ""
		r.replacedBy, r.replacedHow, r.replacedHandle, r.replacedTo = next, how, target.Handle, target.Name
	}
	h.mu.Unlock()
	h.persist(next)
	h.persist(id)
	if h.Notify != nil && !userTask {
		verb := "retried"
		if how == ReplaceReassign {
			verb = "reassigned"
		}
		note := fmt.Sprintf("The user %s the task to %s [task %s]: it went to %s as task %s. Its reply comes back here; do not resend it yourself.", verb, from.Label(), id, target.Label(), next)
		_ = h.Notify.Deliver(context.WithoutCancel(ctx), sessionID, note) // best-effort: the new task itself went out
	}
	return out, nil
}

// replaceTarget is the teammate a Retry or Reassign goes to, looked up in
// the sender's roster (rosterFor). Retry: the task's own agent, refused
// when its handle is gone or now names another agent. Reassign: the agent
// called to, not the task's own, and — for the agent's task — one whose
// mention policy takes the agent.
func (h *Hub) replaceTarget(ctx context.Context, caller string, from Peer, userTask bool, originUser, to, how string) (Peer, error) {
	peers, err := h.rosterFor(ctx, caller, userTask, originUser)
	if err != nil {
		return Peer{}, err
	}
	if how == ReplaceRetry {
		to = NormalizeHandle(from.Handle)
	}
	var target Peer
	found := false
	for _, p := range peers {
		if NormalizeHandle(p.Handle) == to {
			target, found = p, true
			break
		}
	}
	if how == ReplaceRetry {
		switch {
		case !found:
			return Peer{}, ErrTeammateGone
		case target.ID != from.ID:
			return Peer{}, ErrTeammateChanged
		}
		return target, nil
	}
	switch {
	case !found:
		return Peer{}, ErrUnknownTeammate
	case target.ID == from.ID:
		return Peer{}, ErrReassignSame
	}
	if !userTask {
		self, err := h.Dir.Get(ctx, caller)
		if err != nil {
			return Peer{}, err
		}
		if !target.AcceptsFrom(self) {
			return Peer{}, ErrTeammateRefuses
		}
	}
	return target, nil
}
