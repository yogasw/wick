package ticket

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

func cfg(followupSec, resolveSec int64) project.TicketConfig {
	return project.TicketConfig{
		Enabled:             true,
		FollowupAfterSec:    followupSec,
		FollowupPrompt:      "check the ticket",
		AutoResolveAfterSec: resolveSec,
	}
}

func tk(status string, updatedAgo, followupAgo time.Duration, now time.Time) *session.Ticket {
	t := &session.Ticket{Status: status, UpdatedAt: now.Add(-updatedAgo)}
	if followupAgo > 0 {
		t.LastFollowupAt = now.Add(-followupAgo)
	}
	return t
}

func TestNeedsFollowup(t *testing.T) {
	now := time.Now()
	hour := int64(3600)
	cases := []struct {
		name string
		cfg  project.TicketConfig
		t    *session.Ticket
		want bool
	}{
		{"nil ticket", cfg(hour, 0), nil, false},
		{"disabled", project.TicketConfig{FollowupAfterSec: hour}, tk("open", 2*time.Hour, 0, now), false},
		{"timer off", cfg(0, 0), tk("open", 100*time.Hour, 0, now), false},
		{"done never followed up", cfg(hour, 0), tk(session.TicketDone, 5*time.Hour, 0, now), false},
		{"fresh", cfg(hour, 0), tk("open", 10*time.Minute, 0, now), false},
		{"stale first followup", cfg(hour, 0), tk("open", 2*time.Hour, 0, now), true},
		{"stale but recently followed up", cfg(hour, 0), tk("open", 2*time.Hour, 10*time.Minute, now), false},
		{"stale and followup window passed again", cfg(hour, 0), tk("open", 3*time.Hour, 90*time.Minute, now), true},
	}
	for _, c := range cases {
		if got := NeedsFollowup(c.cfg, c.t, now); got != c.want {
			t.Errorf("%s: NeedsFollowup = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNeedsAutoResolve(t *testing.T) {
	now := time.Now()
	week := int64(7 * 24 * 3600)
	cases := []struct {
		name string
		cfg  project.TicketConfig
		t    *session.Ticket
		want bool
	}{
		{"nil ticket", cfg(0, week), nil, false},
		{"disabled", project.TicketConfig{AutoResolveAfterSec: week}, tk("open", 200*24*time.Hour, 0, now), false},
		{"timer off", cfg(0, 0), tk("open", 200*24*time.Hour, 0, now), false},
		{"already done", cfg(0, week), tk(session.TicketDone, 30*24*time.Hour, 0, now), false},
		{"fresh", cfg(0, week), tk("open", 24*time.Hour, 0, now), false},
		{"idle past window", cfg(0, week), tk("open", 8*24*time.Hour, 0, now), true},
	}
	for _, c := range cases {
		if got := NeedsAutoResolve(c.cfg, c.t, now); got != c.want {
			t.Errorf("%s: NeedsAutoResolve = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFollowupMessage(t *testing.T) {
	now := time.Now()
	sess := session.Session{
		ID: "abc123",
		Meta: session.Meta{
			Label: "Payment webhook down",
			Ticket: &session.Ticket{
				Status:    session.TicketInProgress,
				Assignee:  "user-9",
				Fields:    map[string]string{"priority": "high"},
				UpdatedAt: now.Add(-2 * time.Hour),
			},
		},
	}
	msg := FollowupMessage(sess, cfg(3600, 0))
	for _, want := range []string{"abc123", "Payment webhook down", "in_progress", "priority", "high", "check the ticket"} {
		if !strings.Contains(msg, want) {
			t.Errorf("FollowupMessage missing %q in:\n%s", want, msg)
		}
	}
}

func TestSweepOnce(t *testing.T) {
	now := time.Now()
	hour := int64(3600)
	week := int64(7 * 24 * 3600)

	stale := &session.Ticket{Status: "open", UpdatedAt: now.Add(-2 * time.Hour)}
	dead := &session.Ticket{Status: "waiting", UpdatedAt: now.Add(-8 * 24 * time.Hour)}
	fresh := &session.Ticket{Status: "open", UpdatedAt: now.Add(-time.Minute)}

	sessions := map[string]session.Session{
		"s-stale": {ID: "s-stale", Meta: session.Meta{ProjectID: "p1", Ticket: stale}},
		"s-dead":  {ID: "s-dead", Meta: session.Meta{ProjectID: "p1", Ticket: dead}},
		"s-fresh": {ID: "s-fresh", Meta: session.Meta{ProjectID: "p1", Ticket: fresh}},
		"s-plain": {ID: "s-plain", Meta: session.Meta{ProjectID: "p1"}},
	}

	var sent []string
	var saved []string
	d := Deps{
		ListProjects: func() ([]project.Project, error) {
			return []project.Project{{Meta: project.Meta{ID: "p1", Ticket: cfg(hour, week)}}}, nil
		},
		ListSessions: func(projectID string) ([]string, error) {
			return []string{"s-stale", "s-dead", "s-fresh", "s-plain"}, nil
		},
		LoadSession: func(id string) (session.Session, error) { return sessions[id], nil },
		SaveTicket: func(id string, t *session.Ticket) error {
			saved = append(saved, id)
			s := sessions[id]
			s.Meta.Ticket = t
			sessions[id] = s
			return nil
		},
		SendFollowup: func(sessionID, text string) error {
			sent = append(sent, sessionID)
			return nil
		},
	}

	sweepOnce(context.Background(), d, now)

	if len(sent) != 1 || sent[0] != "s-stale" {
		t.Fatalf("followup sent to %v, want [s-stale]", sent)
	}
	if sessions["s-dead"].Meta.Ticket.Status != session.TicketDone {
		t.Fatalf("dead ticket not auto-resolved: %+v", sessions["s-dead"].Meta.Ticket)
	}
	if sessions["s-stale"].Meta.Ticket.LastFollowupAt.IsZero() {
		t.Fatal("LastFollowupAt not stamped after followup")
	}
	// Fresh session untouched; plain session ADOPTED as an open ticket.
	for _, id := range saved {
		if id == "s-fresh" {
			t.Fatalf("session %s should not be saved", id)
		}
	}
	adopted := sessions["s-plain"].Meta.Ticket
	if adopted == nil || adopted.Status != session.TicketOpen {
		t.Fatalf("plain session not adopted as open ticket: %+v", adopted)
	}

	// Second pass immediately after: nothing new (followup guard).
	sent = nil
	sweepOnce(context.Background(), d, now.Add(time.Minute))
	if len(sent) != 0 {
		t.Fatalf("followup re-sent within guard window: %v", sent)
	}
}
