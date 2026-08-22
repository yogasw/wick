package session

// Ticket statuses. Fixed set for v1 — the kanban board renders one column
// per status in this order.
const (
	TicketOpen       = "open"
	TicketInProgress = "in_progress"
	TicketWaiting    = "waiting"
	TicketDone       = "done"
)

// TicketStatuses lists every valid status in board column order.
var TicketStatuses = []string{TicketOpen, TicketInProgress, TicketWaiting, TicketDone}

// ValidTicketStatus reports whether s is one of the fixed ticket statuses.
func ValidTicketStatus(s string) bool {
	for _, v := range TicketStatuses {
		if s == v {
			return true
		}
	}
	return false
}
