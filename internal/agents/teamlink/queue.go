package teamlink

import (
	"context"
	"fmt"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// The line of team tasks waiting for their turn in a teammate's chat
// (TurnGate), made visible on both sides: the sender's task card reads
// "waiting for its turn at @x (#2 in line)" (TaskView.GatePos), and the
// teammate's chat shows a "Queued (n)" strip over its composer (QueueAt).

// Waiting lists the tasks waiting for their turn in sessionID, the next
// one first.
func (g *TurnGate) Waiting(sessionID string) []a2a.TaskID {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[sessionID]
	if s == nil {
		return nil
	}
	out := make([]a2a.TaskID, 0, len(s.waiting))
	for _, w := range s.waiting {
		out = append(out, w.id)
	}
	return out
}

// QueuedTask is one task in a chat's line, as the strip shows it.
type QueuedTask struct {
	TaskID     string    `json:"task_id"`
	Pos        int       `json:"pos"`
	FromHandle string    `json:"from_handle"`
	FromName   string    `json:"from_name"`
	Title      string    `json:"title"`
	Since      time.Time `json:"since"`
	// Revisions is how many amends wait with it (Amend).
	Revisions int `json:"revisions,omitempty"`
}

// QueueAt is the line of tasks waiting for their turn in sessionID,
// next first; nil when none (or GateLine is not wired).
func (h *Hub) QueueAt(ctx context.Context, sessionID string) []QueuedTask {
	if h.GateLine == nil || sessionID == "" {
		return nil
	}
	ids := h.GateLine(sessionID)
	if len(ids) == 0 {
		return nil
	}
	type row struct {
		q      QueuedTask
		caller string
	}
	var rows []row
	h.mu.Lock()
	for _, id := range ids {
		ref := h.tasks[id]
		if ref == nil || ref.finished || ref.canceled {
			continue
		}
		rows = append(rows, row{QueuedTask{TaskID: string(id), Title: ref.title, Since: ref.started, Revisions: len(ref.revisions)}, ref.callerAgentID})
	}
	h.mu.Unlock()
	out := make([]QueuedTask, 0, len(rows))
	for i, r := range rows {
		r.q.Pos = i + 1
		if p, err := h.Dir.Get(ctx, r.caller); err == nil {
			r.q.FromHandle, r.q.FromName = p.Handle, p.Name
		}
		out = append(out, r.q)
	}
	return out
}

// gatePosLocked is task id's place in the line of its teammate's chat
// (1 = next), 0 when it is not waiting there. h.mu is held; GateLine
// only reads the gate.
func (h *Hub) gatePosLocked(id a2a.TaskID, ref *taskRef) int {
	if h.GateLine == nil || ref.finished || ref.canceled || ref.turnTaken {
		return 0
	}
	in, ok := h.inflight[ref.agentID][id]
	if !ok || in.session == "" {
		return 0
	}
	for i, w := range h.GateLine(in.session) {
		if w == id {
			return i + 1
		}
	}
	return 0
}

// gateNote is the sender's line for a task waiting at the gate.
func gateNote(handle string, pos int) string {
	return fmt.Sprintf("waiting for its turn at @%s (#%d in line)", handle, pos)
}

// openTaskToLocked is the caller session's task to target that has not
// started its turn yet (auto-merge, Send): "" for none.
func (h *Hub) openTaskToLocked(callerID, callerSession, targetID string) a2a.TaskID {
	if callerSession == "" {
		return ""
	}
	var best a2a.TaskID
	var at time.Time
	for id, ref := range h.tasks {
		if ref.callerSession != callerSession || ref.callerAgentID != callerID || ref.agentID != targetID ||
			ref.finished || ref.canceled || ref.turnTaken || ref.claimed || ref.userTask() {
			continue
		}
		// Only a task waiting in its teammate's line (TurnGate): one still
		// being registered has nothing to merge into yet, and without the
		// gate there is no telling whether its turn started.
		if h.gatePosLocked(id, ref) == 0 {
			continue
		}
		if best == "" || ref.started.After(at) {
			best, at = id, ref.started
		}
	}
	return best
}
