package agents

import (
	"encoding/json"

	"github.com/yogasw/wick/internal/agents/ticket"
)

// evTicketChanged is the bus signal that a ticket was written: created,
// edited, moved, assigned, deleted, or a chat attached to or detached
// from it. /stream/sessions forwards it as a `ticket` event to the
// callers who may see the project, and an open board refetches on it
// instead of polling.
const evTicketChanged = "ticket_changed"

// ticketSignal is the whole payload. The board itself stays REST: the
// signal names which project's board is stale and nothing about what
// changed, so no title, field or note ever rides the stream.
type ticketSignal struct {
	ProjectID string `json:"project_id"`
	TicketID  string `json:"ticket_id"`
}

// ticketStreamEmitter tees every ticket event into the broadcaster after
// handing it to next (the webhook dispatcher).
type ticketStreamEmitter struct{ next ticket.Emitter }

// WithTicketStreamSignal wraps the process-wide ticket emitter so every
// ticket write also signals open boards. Pass the result to
// ticket.SetEmitter.
func WithTicketStreamSignal(next ticket.Emitter) ticket.Emitter {
	return ticketStreamEmitter{next: next}
}

func (e ticketStreamEmitter) Emit(ev ticket.Event) {
	if e.next != nil {
		e.next.Emit(ev)
	}
	publishTicketSignal(globalBcast, ev)
}

// publishTicketSignal publishes ev's signal on the global key. A board
// button click carries no ticket (it is about the list, and changes
// nothing), so it is not a change.
func publishTicketSignal(b *Broadcaster, ev ticket.Event) {
	if b == nil || ev.Event == ticket.EventAction || ev.Event == ticket.EventBoardAction {
		return
	}
	pid := ev.ProjectID
	if pid == "" {
		pid = ev.Ticket.ProjectID
	}
	if pid == "" || ev.Ticket.ID == "" {
		return
	}
	body, _ := json.Marshal(ticketSignal{ProjectID: pid, TicketID: ev.Ticket.ID})
	b.PublishRaw("", "", evTicketChanged, string(body))
}

// projectTicketSignal re-reads a bus signal for one /stream/sessions
// caller: ok only when the caller may open that project's board (the
// same project check the board's REST endpoint sits behind).
func projectTicketSignal(ev Event, access projectAccess) (string, bool) {
	var s ticketSignal
	if err := json.Unmarshal([]byte(ev.Data), &s); err != nil || s.ProjectID == "" || !access.allowProject(s.ProjectID) {
		return "", false
	}
	b, _ := json.Marshal(s)
	return string(b), true
}
