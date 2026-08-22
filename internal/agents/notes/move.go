package notes

import (
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// MoveForSession carries a session's notes across when the session changes
// ticket: attach (from "" to a ticket), move (ticket to ticket), or detach
// (ticket back to ""). Wired into the ticket package at boot via
// ticket.SetNotesMover.
//
// Without this a move would silently orphan notes — the scope a session
// resolves to changes, so everything written before it becomes invisible
// with no way for a reader to tell it ever existed.
//
// On a DETACH only the notes this session authored travel back; notes
// written from other sessions on that ticket stay with the ticket, which is
// whose work they describe. On an ATTACH or a MOVE everything in the
// session's own scope goes to the destination, because a loose session's
// notes are all its own by definition.
//
// Each moved note keeps its id and timestamps, and gains a provenance line
// so a reader can see where it came from — a note whose context silently
// changed is worse than one that says so.
func MoveForSession(layout config.Layout, sessionID, fromTicketID, toTicketID string) error {
	if sessionID == "" || fromTicketID == toTicketID {
		return nil
	}
	from, err := scopeFor(layout, sessionID, fromTicketID)
	if err != nil {
		return err
	}
	to, err := scopeFor(layout, sessionID, toTicketID)
	if err != nil {
		return err
	}

	list, err := List(layout, from)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return nil
	}

	detaching := fromTicketID != "" && toTicketID == ""
	moved := 0
	for _, n := range list {
		// Leave other people's ticket notes on the ticket when detaching.
		if detaching && n.SourceSessionID != "" && n.SourceSessionID != sessionID {
			continue
		}
		if detaching && n.SourceSessionID == "" {
			continue // authored on the ticket itself, not by this session
		}
		n.SourceSessionID = sessionID
		n.MovedAt = time.Now().UTC()
		if werr := write(layout, to, n); werr != nil {
			return werr
		}
		if derr := Delete(layout, from, n.ID); derr != nil {
			return derr
		}
		moved++
	}
	if moved == 0 {
		return nil
	}
	return nil
}

// scopeFor turns "this session, on that ticket (or none)" into a scope.
func scopeFor(layout config.Layout, sessionID, ticketID string) (Scope, error) {
	if ticketID == "" {
		return Scope{SessionID: sessionID}, nil
	}
	projectID, err := projectOfSession(layout, sessionID)
	if err != nil {
		return Scope{}, err
	}
	if !ticket.Exists(layout, projectID, ticketID) {
		return Scope{}, fmt.Errorf("ticket %q not found in project %s", ticketID, projectID)
	}
	return Scope{ProjectID: projectID, TicketID: ticketID}, nil
}
