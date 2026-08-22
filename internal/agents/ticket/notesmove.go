package ticket

import "github.com/yogasw/wick/internal/agents/config"

// NotesMover moves a session's notes when the session changes ticket.
//
// A func rather than a direct call because internal/agents/notes already
// imports this package (to resolve a session's scope), so calling it back
// from here would close an import cycle. The server wires the real
// implementation at boot; nil means "leave notes where they are", which is
// what tests that do not care about notes get.
type NotesMover func(layout config.Layout, sessionID, fromTicketID, toTicketID string) error

var notesMover NotesMover

// SetNotesMover installs the notes-follow-the-session hook. Call once at
// boot, before anything can attach or detach.
//
// Why this exists: notes are scoped to the ticket when a session belongs to
// one, and to the session when it does not. Move the session without moving
// its notes and they silently stop being visible anywhere — the reader has
// no way to tell they were ever written. So every attach, move, and detach
// carries them along.
func SetNotesMover(fn NotesMover) { notesMover = fn }

// moveNotes runs the hook, ignoring the "not wired" case.
func moveNotes(layout config.Layout, sessionID, fromTicketID, toTicketID string) error {
	if notesMover == nil || sessionID == "" || fromTicketID == toTicketID {
		return nil
	}
	return notesMover(layout, sessionID, fromTicketID, toTicketID)
}
