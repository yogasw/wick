package ticket

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// sweepInterval is how often the sweeper re-scans ticket-enabled
// projects. Followup windows are measured in minutes-to-hours, so a
// minute of slack is invisible.
const sweepInterval = time.Minute

// Deps are the sweeper's injected collaborators. Funcs, not interfaces:
// the server wires them from layout/pool at boot, tests pass fakes.
type Deps struct {
	ListProjects func() ([]project.Project, error)
	// ListSessions returns the session IDs belonging to one project.
	ListSessions func(projectID string) ([]string, error)
	LoadSession  func(id string) (session.Session, error)
	// SaveTicket persists a session's mutated ticket.
	SaveTicket func(sessionID string, t *session.Ticket) error
	// SendFollowup delivers the followup turn to the session's agent,
	// spawning it if idle (wired to pool.Send with role "user",
	// source "ticket").
	SendFollowup func(sessionID, text string) error
}

// Start runs the sweeper loop until ctx is cancelled. Projects with
// ticket mode off (or both timers at 0) cost one meta read per tick and
// nothing else.
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
// over followup when both are due — a ticket dead past the resolve
// window gets closed, not re-nagged.
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
		ids, err := d.ListSessions(p.Meta.ID)
		if err != nil {
			l.Warn().Err(err).Str("project", p.Meta.ID).Msg("list sessions failed")
			continue
		}
		for _, sid := range ids {
			if ctx.Err() != nil {
				return
			}
			sess, err := d.LoadSession(sid)
			if err != nil {
				continue
			}
			// Adopt: a session in a ticket-enabled project that has no
			// ticket yet (created before enable, or seconds ago) becomes
			// an open ticket now, clocked from its last activity. This
			// is what makes ticket mode retroactive with no create-path
			// hook and no migration.
			if sess.Meta.Ticket == nil {
				nt := &session.Ticket{Status: session.TicketOpen, UpdatedAt: sess.Meta.LastActive}
				if nt.UpdatedAt.IsZero() {
					nt.UpdatedAt = now
				}
				if err := d.SaveTicket(sid, nt); err != nil {
					l.Warn().Err(err).Str("session", sid).Msg("ticket adopt save failed")
				}
				continue // timers start from this pass; next tick evaluates them
			}
			t := sess.Meta.Ticket
			switch {
			case NeedsAutoResolve(cfg, t, now):
				t.Status = session.TicketDone
				t.UpdatedAt = now
				if err := d.SaveTicket(sid, t); err != nil {
					l.Warn().Err(err).Str("session", sid).Msg("auto-resolve save failed")
					continue
				}
				l.Info().Str("session", sid).Str("project", p.Meta.ID).Msg("ticket auto-resolved (idle past window)")
			case NeedsFollowup(cfg, t, now):
				if err := d.SendFollowup(sid, FollowupMessage(sess, cfg)); err != nil {
					l.Warn().Err(err).Str("session", sid).Msg("followup send failed")
					continue
				}
				t.LastFollowupAt = now
				if err := d.SaveTicket(sid, t); err != nil {
					l.Warn().Err(err).Str("session", sid).Msg("followup stamp save failed")
					continue
				}
				l.Info().Str("session", sid).Str("project", p.Meta.ID).Msg("ticket followup sent to agent")
			}
		}
	}
}
