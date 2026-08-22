package ticket

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// sweepInterval is how often the sweeper re-scans ticket-enabled
// projects. Follow-up windows are measured in minutes-to-hours, so a
// minute of slack is invisible.
const sweepInterval = time.Minute

// Deps are the sweeper's injected collaborators. Funcs, not interfaces:
// the server wires them at boot, tests pass fakes. SendFollowup is a func
// so this package never imports the pool.
type Deps struct {
	Layout       config.Layout
	ListProjects func() ([]project.Project, error)
	// SendFollowup delivers the follow-up turn to one of the ticket's
	// sessions, spawning the agent if it is idle (wired to pool.Send with
	// role "user", source "ticket").
	SendFollowup func(sessionID, text string) error
}

// NeedsFollowup reports whether a ticket is stale enough to wake its
// agent: ticket mode on, follow-up timer on, not done, no ticket edit for
// a full window, and the previous follow-up (if any) at least a window
// ago — so a stuck ticket is nudged once per window, not once per tick.
func NeedsFollowup(cfg project.TicketConfig, t Ticket, now time.Time) bool {
	if !cfg.Enabled || cfg.FollowupAfterSec <= 0 || t.Status == StatusDone {
		return false
	}
	window := time.Duration(cfg.FollowupAfterSec) * time.Second
	if now.Sub(t.UpdatedAt) <= window {
		return false
	}
	return t.LastFollowupAt.IsZero() || now.Sub(t.LastFollowupAt) > window
}

// NeedsAutoResolve reports whether a ticket has been idle long enough to
// close on its own.
func NeedsAutoResolve(cfg project.TicketConfig, t Ticket, now time.Time) bool {
	if !cfg.Enabled || cfg.AutoResolveAfterSec <= 0 || t.Status == StatusDone {
		return false
	}
	return now.Sub(t.UpdatedAt) > time.Duration(cfg.AutoResolveAfterSec)*time.Second
}

// FollowupMessage renders the turn sent to a ticket's agent when the
// ticket goes stale: a snapshot plus the project's follow-up prompt. The
// agent decides the action — post to a channel, ping the assignee, close
// the ticket — because only it can see the conversation so far.
func FollowupMessage(t Ticket, cfg project.TicketConfig) string {
	var b strings.Builder
	b.WriteString("[ticket followup] This ticket has gone stale (no ticket update within the follow-up window).\n\n")
	b.WriteString("Ticket snapshot:\n")
	fmt.Fprintf(&b, "- id: %s\n", t.ID)
	if t.Title != "" {
		fmt.Fprintf(&b, "- title: %s\n", t.Title)
	}
	fmt.Fprintf(&b, "- status: %s\n", t.Status)
	if t.Assignee != "" {
		fmt.Fprintf(&b, "- assignee: %s\n", t.Assignee)
	}
	keys := make([]string, 0, len(t.Fields))
	for k := range t.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic rendering
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %s\n", k, t.Fields[k])
	}
	fmt.Fprintf(&b, "- sessions: %d\n", len(t.Sessions))
	fmt.Fprintf(&b, "- last ticket update: %s\n", t.UpdatedAt.UTC().Format(time.RFC3339))
	b.WriteString("\nInstruction from the project's follow-up policy:\n")
	prompt := strings.TrimSpace(cfg.FollowupPrompt)
	if prompt == "" {
		prompt = "Review this ticket's latest state and update it. If it is blocked or needs a human, say so briefly."
	}
	b.WriteString(prompt)
	b.WriteString("\n\nRead the ticket's notes and update its status through the tickets connector after acting. " +
		"Open your reply with [silent] if no human needs to see it.")
	return b.String()
}

// Start runs the sweeper until ctx is cancelled. A project with ticket
// mode off costs one already-loaded meta read per tick and nothing else.
func Start(ctx context.Context, d Deps) {
	l := log.With().Str("component", "ticketsweep").Logger()
	l.Debug().Dur("interval", sweepInterval).Msg("started")
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			l.Debug().Msg("stopped")
			return
		case <-ticker.C:
			sweepOnce(ctx, d, time.Now())
		}
	}
}

// sweepOnce scans every ticket-enabled project once. Auto-resolve wins
// over follow-up when both are due: a ticket dead past the resolve window
// gets closed, not nagged.
func sweepOnce(ctx context.Context, d Deps, now time.Time) {
	l := log.With().Str("component", "ticketsweep").Logger()
	projects, err := d.ListProjects()
	if err != nil {
		l.Warn().Err(err).Msg("list projects failed")
		return
	}
	for _, p := range projects {
		cfg := p.Meta.Ticket
		if !cfg.Enabled || (cfg.FollowupAfterSec <= 0 && cfg.AutoResolveAfterSec <= 0) {
			continue
		}
		tickets, lerr := List(d.Layout, p.Meta.ID)
		if lerr != nil {
			l.Warn().Err(lerr).Str("project", p.Meta.ID).Msg("list tickets failed")
			continue
		}
		for _, t := range tickets {
			if ctx.Err() != nil {
				return
			}
			switch {
			case NeedsAutoResolve(cfg, t, now):
				t.Status = StatusDone
				t.UpdatedAt = now
				if serr := SaveKeepingTimestamp(d.Layout, t); serr != nil {
					l.Warn().Err(serr).Str("ticket", t.ID).Msg("auto-resolve save failed")
					continue
				}
				l.Info().Str("ticket", t.ID).Str("project", p.Meta.ID).Msg("ticket auto-resolved (idle past window)")

			case NeedsFollowup(cfg, t, now):
				// No session means no agent to wake. Stamping the attempt
				// anyway would silently consume the window, so leave the
				// ticket untouched — it still auto-resolves on schedule.
				if len(t.Sessions) == 0 {
					continue
				}
				// The most recently attached session is where the work is.
				target := t.Sessions[len(t.Sessions)-1]
				if serr := d.SendFollowup(target, FollowupMessage(t, cfg)); serr != nil {
					l.Warn().Err(serr).Str("ticket", t.ID).Str("session", target).Msg("followup send failed")
					continue
				}
				t.LastFollowupAt = now
				if serr := SaveKeepingTimestamp(d.Layout, t); serr != nil {
					l.Warn().Err(serr).Str("ticket", t.ID).Msg("followup stamp save failed")
					continue
				}
				l.Info().Str("ticket", t.ID).Str("session", target).Msg("ticket followup sent to agent")
			}
		}
	}
}
