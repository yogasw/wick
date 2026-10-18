package teamlink

import (
	"fmt"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// What a working task's teammate waits on besides its own turn: the pool
// restarting it after an idle kill, or a free slot. Read live from the
// pool (WaitOf), never stored: the task stays "working" and the cards show
// "Restarting · retry 1/2" or "Queued · #2" instead of a silent spinner.

// TargetWait is a teammate's wait, as WaitOf reports it. Zero = none.
type TargetWait struct {
	// RestartAttempt is the idle-kill retry under way (1-based) out of
	// RestartMax; 0 when not restarting.
	RestartAttempt, RestartMax int
	// QueuePos is the teammate's place in the pool queue (1 = next).
	QueuePos int
}

// Note is the wait in one line for an agent waiting on the task, "" for
// none: "target restarting, retry 1/2".
func (w TargetWait) Note() string {
	switch {
	case w.RestartAttempt > 0:
		return fmt.Sprintf("target restarting after an idle timeout, retry %d/%d", w.RestartAttempt, w.RestartMax)
	case w.QueuePos > 0:
		return fmt.Sprintf("target queued for a free slot (#%d)", w.QueuePos)
	}
	return ""
}

// waitHub is the Hub's part for target waits.
type waitHub struct {
	// WaitOf reports what the teammate's chat (chat; "" = its main chat)
	// waits on. Reads the pool, so never called with h.mu held. nil =
	// never waits.
	WaitOf func(to Peer, chat string) TargetWait
	waitMu sync.Mutex
	// shownWait is the wait last sent per working task, so RefreshWaiting
	// re-sends only the tasks whose wait moved.
	shownWait map[a2a.TaskID]TargetWait
}

// fillWait sets v's wait from WaitOf. Only a working task waits.
func (h *Hub) fillWait(v *TaskView) {
	if h.WaitOf == nil || v.State != "working" {
		return
	}
	w := h.WaitOf(v.to, v.ChatID)
	v.RestartAttempt, v.RestartMax, v.QueuePos = w.RestartAttempt, w.RestartMax, w.QueuePos
	if n := w.Note(); n != "" || v.GatePos == 0 {
		v.WaitNote = n
	}
	h.waitMu.Lock()
	if h.shownWait == nil {
		h.shownWait = map[a2a.TaskID]TargetWait{}
	}
	h.shownWait[a2a.TaskID(v.TaskID)] = w
	h.waitMu.Unlock()
}

// RefreshWaiting sends OnTaskChange again for every working task whose
// teammate's wait moved (a restart began or came back, the queue moved),
// so the cards follow without a poll. Call it when the pool says a wait
// changed.
func (h *Hub) RefreshWaiting() {
	if h.OnTaskChange == nil || h.WaitOf == nil {
		return
	}
	type item struct {
		session string
		v       TaskView
	}
	h.saveMu.Lock()
	defer h.saveMu.Unlock()
	h.mu.Lock()
	now := h.now()
	var items []item
	for id, ref := range h.tasks {
		if ref.callerSession == "" || ref.started.IsZero() || ref.finished || ref.canceled {
			continue
		}
		v := h.viewLocked(id, ref, now)
		if v.State == "working" {
			items = append(items, item{ref.callerSession, v})
		}
	}
	h.mu.Unlock()
	for _, it := range items {
		h.waitMu.Lock()
		before, seen := h.shownWait[a2a.TaskID(it.v.TaskID)]
		h.waitMu.Unlock()
		h.fillWait(&it.v)
		after := TargetWait{RestartAttempt: it.v.RestartAttempt, RestartMax: it.v.RestartMax, QueuePos: it.v.QueuePos}
		if seen && before == after || !seen && after == (TargetWait{}) {
			continue
		}
		h.OnTaskChange(it.session, it.v)
	}
}

// noteWait tells the agent waiting on a working task what its teammate
// waits on ("target restarting after an idle timeout, retry 1/2"), so a
// restart or a queue does not read as a failure. The state stays working.
func (h *Hub) noteWait(out *Result) {
	if out == nil || out.State != "working" || h.WaitOf == nil {
		return
	}
	h.mu.Lock()
	ref := h.tasks[a2a.TaskID(out.TaskID)]
	var to Peer
	var chat string
	if ref != nil {
		to, chat = ref.to, ref.chat
	}
	h.mu.Unlock()
	if ref == nil {
		return
	}
	if n := h.WaitOf(to, chat).Note(); n != "" {
		out.Note = "Still working: " + n + ". Keep waiting, it has not failed. " + out.Note
	}
}
