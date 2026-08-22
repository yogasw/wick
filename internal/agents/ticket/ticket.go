// Package ticket implements the ticket-mode automation on top of
// sessions: staleness decisions and the background sweeper that wakes an
// agent to follow up a stale ticket (project.TicketConfig.FollowupPrompt
// decides what the agent should do) and auto-resolves tickets idle past
// the project's window.
//
// The package owns no storage: session tickets live in session.Meta,
// project config in project.Meta. The sweeper receives its collaborators
// as injected funcs (Deps) so it stays free of pool imports and trivially
// testable.
package ticket

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// NeedsFollowup reports whether a ticket is stale enough to wake the
// agent: ticket mode + followup timer on, ticket not done, no ticket edit
// for FollowupAfterSec, and the previous followup (if any) older than
// another full window — so a stuck ticket is re-followed every window,
// not every sweep tick.
func NeedsFollowup(cfg project.TicketConfig, t *session.Ticket, now time.Time) bool {
	if t == nil || !cfg.Enabled || cfg.FollowupAfterSec <= 0 || t.Status == session.TicketDone {
		return false
	}
	window := time.Duration(cfg.FollowupAfterSec) * time.Second
	if now.Sub(t.UpdatedAt) <= window {
		return false
	}
	return t.LastFollowupAt.IsZero() || now.Sub(t.LastFollowupAt) > window
}

// NeedsAutoResolve reports whether a ticket has been idle long enough to
// close automatically: ticket mode + auto-resolve timer on, not already
// done, and no ticket edit for AutoResolveAfterSec.
func NeedsAutoResolve(cfg project.TicketConfig, t *session.Ticket, now time.Time) bool {
	if t == nil || !cfg.Enabled || cfg.AutoResolveAfterSec <= 0 || t.Status == session.TicketDone {
		return false
	}
	return now.Sub(t.UpdatedAt) > time.Duration(cfg.AutoResolveAfterSec)*time.Second
}

// FollowupMessage renders the turn sent to the session's agent when its
// ticket goes stale: a ticket snapshot followed by the project's
// followup prompt. The agent — not wick — decides the action (post to a
// channel, ping the assignee, close the ticket via wick_ticket_set, …).
func FollowupMessage(sess session.Session, cfg project.TicketConfig) string {
	t := sess.Meta.Ticket
	var b strings.Builder
	b.WriteString("[ticket followup] This session's ticket has gone stale (no ticket update within the follow-up window).\n\n")
	b.WriteString("Ticket snapshot:\n")
	fmt.Fprintf(&b, "- session_id: %s\n", sess.ID)
	if sess.Meta.Label != "" {
		fmt.Fprintf(&b, "- title: %s\n", sess.Meta.Label)
	}
	fmt.Fprintf(&b, "- status: %s\n", t.Status)
	if t.Assignee != "" {
		fmt.Fprintf(&b, "- assignee: %s\n", t.Assignee)
	}
	// Stable field order so the rendered turn is deterministic.
	keys := make([]string, 0, len(t.Fields))
	for k := range t.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, t.Fields[k])
	}
	fmt.Fprintf(&b, "- last ticket update: %s\n", t.UpdatedAt.UTC().Format(time.RFC3339))
	b.WriteString("\nInstruction from the project's follow-up policy:\n")
	prompt := strings.TrimSpace(cfg.FollowupPrompt)
	if prompt == "" {
		prompt = "Review this ticket's latest state and update its status via wick_ticket_set. If it is blocked or needs a human, say so briefly."
	}
	b.WriteString(prompt)
	b.WriteString("\n\nUse wick_ticket_set to update the ticket after acting. Open your reply with [silent] if no human needs to see it.")
	return b.String()
}
