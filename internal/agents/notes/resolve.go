package notes

import (
	"fmt"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// Resolve returns the notes scope a session reads and writes.
//
// A session attached to a ticket resolves to the TICKET's scope, so every
// session on that ticket shares one set of notes. That sharing is the
// whole point: when an agent loses the thread you start a fresh session on
// the same ticket, and the notes are already there.
//
// A session attached to nothing resolves to its own scope.
//
// The session's TicketID is only a hint. It is confirmed against the
// ticket's own Sessions list, and a pointer the ticket does not claim is
// ignored — reading another ticket's notes because of a stale field would
// be worse than reading none.
func Resolve(layout config.Layout, sessionID string) (Scope, error) {
	if sessionID == "" {
		return Scope{}, fmt.Errorf("session id is required")
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return Scope{}, fmt.Errorf("load session %q: %w", sessionID, err)
	}
	own := Scope{SessionID: sessionID}
	if sess.Meta.ProjectID == "" {
		return own, nil
	}
	// Trust the ticket, not the back-pointer: FindBySession reads the
	// Sessions list that is the record.
	if tk, ok := ticket.FindBySession(layout, sess.Meta.ProjectID, sessionID); ok {
		return Scope{ProjectID: tk.ProjectID, TicketID: tk.ID}, nil
	}
	return own, nil
}

// projectOfSession returns the project a session belongs to. Used by the
// move path, which needs it to build a ticket scope.
func projectOfSession(layout config.Layout, sessionID string) (string, error) {
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return "", fmt.Errorf("load session %q: %w", sessionID, err)
	}
	if sess.Meta.ProjectID == "" {
		return "", fmt.Errorf("session %q belongs to no project", sessionID)
	}
	return sess.Meta.ProjectID, nil
}
