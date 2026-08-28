// Package ticketprompt builds the short prompt block that tells an agent
// which ticket its session is on and how many notes are waiting.
//
// Its own package because it reads BOTH stores: internal/agents/notes
// already imports internal/agents/ticket (to resolve a session's scope), so
// putting this in either one would close an import cycle.
package ticketprompt

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// bodyExcerptLen bounds how much of the ticket's description rides in the
// prompt. Enough for repro steps and links to register; anything longer is
// a document, and documents are read through ticket_get.
const bodyExcerptLen = 600

// excerpt returns at most n bytes of s, cut on a rune boundary via the
// trailing-space trim, with surrounding whitespace dropped.
func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	// Never split a UTF-8 sequence: back up to the last full rune.
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut) + "…"
}

// Pointer returns the short system-prompt block telling a session which
// ticket it is working on and how many notes are waiting — a count and an
// id, never the note bodies.
//
// That distinction is the whole point. A ticket collects notes for as long
// as the work lasts; inlining them would charge that growing cost on every
// turn, forever. A pointer costs the same whether there is one note or
// fifty, and the agent reads the ones that matter through the notes
// connector.
//
// Returns "" when there is nothing worth saying: no ticket and no notes.
func Pointer(layout config.Layout, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return ""
	}

	var tk ticket.Ticket
	hasTicket := false
	if sess.Meta.ProjectID != "" {
		// FindBySession, not the session's TicketID: the ticket's own
		// session list is the record, and a stale back-pointer must not
		// make us announce a ticket this session is not on.
		if found, ok := ticket.FindBySession(layout, sess.Meta.ProjectID, sessionID); ok {
			tk, hasTicket = found, true
		}
	}

	scope := notes.Scope{SessionID: sessionID}
	if hasTicket {
		scope = notes.Scope{ProjectID: tk.ProjectID, TicketID: tk.ID}
	}
	count, cerr := notes.Counts(layout, scope)
	if cerr != nil {
		count = notes.Count{}
	}
	if !hasTicket && count.Visible == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Ticket and notes\n\n")
	if hasTicket {
		fmt.Fprintf(&b, "This session is working on ticket %s", tk.ID)
		if tk.Title != "" {
			fmt.Fprintf(&b, " — %q", tk.Title)
		}
		fmt.Fprintf(&b, " (status: %s", tk.Status)
		if n := len(tk.Sessions); n > 1 {
			fmt.Fprintf(&b, ", %d sessions", n)
		}
		b.WriteString(").\n")
		// The body rides along, excerpted: unlike notes it does not grow
		// with the work, so a bounded slice of it is a fixed cost — and it
		// is exactly the context that makes "what is this chat about?"
		// answerable without a tool call. The full text (and the fields)
		// stay one ticket_get away.
		if body := excerpt(tk.Body, bodyExcerptLen); body != "" {
			fmt.Fprintf(&b, "Its description:\n\n%s\n\n", body)
			if len(tk.Body) > bodyExcerptLen {
				b.WriteString("(truncated — ticket_get returns the full description and fields.)\n")
			}
		}
	}
	switch {
	case count.Visible == 0:
		b.WriteString("It has no notes yet. ")
	case count.OpenTasks > 0:
		fmt.Fprintf(&b, "It carries %d note(s), %d of them unchecked. ", count.Visible, count.OpenTasks)
	default:
		fmt.Fprintf(&b, "It carries %d note(s). ", count.Visible)
	}
	b.WriteString("Notes are what earlier sessions left behind: findings, decisions, dead ends.\n\n")
	if count.Visible > 0 {
		b.WriteString("Read them with the notes connector (notes_list) before continuing work here — ")
		b.WriteString("they are not in this prompt.\n")
	}
	b.WriteString("Write one (notes_add) whenever you learn something a later session would otherwise rediscover. ")
	b.WriteString("See the wick-notes skill.")
	if hasTicket {
		b.WriteString(" Keep the ticket's status current through the tickets connector (wick-tickets skill).")
		b.WriteString("\n\nThis ticket is what the session is about: unless the user plainly says otherwise, ")
		b.WriteString("assume every request here concerns it. Read the ticket before anything else — ")
		b.WriteString("ticket_get for the description and fields, notes_list for its notes — ")
		b.WriteString("then check your skills for one that covers the ask (logging time, reports, and the like) and follow it. ")
		b.WriteString("Do not go digging through other sessions or unrelated data; ")
		b.WriteString("ad-hoc exploration is a last resort when neither the ticket, its notes, nor a skill covers the ask.")
	}
	return b.String()
}
