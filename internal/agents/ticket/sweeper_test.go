package ticket

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
)

func cfg(followupSec, resolveSec int64) project.TicketConfig {
	return project.TicketConfig{
		Enabled:             true,
		FollowupAfterSec:    followupSec,
		FollowupPrompt:      "check the ticket",
		AutoResolveAfterSec: resolveSec,
	}
}

func tk(status string, updatedAgo, followupAgo time.Duration, now time.Time) Ticket {
	t := Ticket{ID: "T-TEST", ProjectID: "p1", Status: status, UpdatedAt: now.Add(-updatedAgo)}
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
		t    Ticket
		want bool
	}{
		{"disabled", project.TicketConfig{FollowupAfterSec: hour}, tk("open", 2*time.Hour, 0, now), false},
		{"timer off", cfg(0, 0), tk("open", 100*time.Hour, 0, now), false},
		{"done", cfg(hour, 0), tk(StatusDone, 5*time.Hour, 0, now), false},
		{"fresh", cfg(hour, 0), tk("open", 10*time.Minute, 0, now), false},
		{"stale, first followup", cfg(hour, 0), tk("open", 2*time.Hour, 0, now), true},
		{"stale, just followed up", cfg(hour, 0), tk("open", 2*time.Hour, 10*time.Minute, now), false},
		{"stale, window passed again", cfg(hour, 0), tk("open", 3*time.Hour, 90*time.Minute, now), true},
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
		t    Ticket
		want bool
	}{
		{"disabled", project.TicketConfig{AutoResolveAfterSec: week}, tk("open", 200*24*time.Hour, 0, now), false},
		{"timer off", cfg(0, 0), tk("open", 200*24*time.Hour, 0, now), false},
		{"already done", cfg(0, week), tk(StatusDone, 30*24*time.Hour, 0, now), false},
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
	item := Ticket{
		ID: "T-4F2A", ProjectID: "p1", Title: "Payment webhook down",
		Status: StatusInProgress, Assignee: "user-9",
		Fields: map[string]string{"priority": "high"}, UpdatedAt: now.Add(-2 * time.Hour),
	}
	msg := FollowupMessage(item, cfg(3600, 0))
	for _, want := range []string{"T-4F2A", "Payment webhook down", "in_progress", "priority", "high", "check the ticket"} {
		if !strings.Contains(msg, want) {
			t.Errorf("FollowupMessage missing %q in:\n%s", want, msg)
		}
	}
}

// A ticket with no sessions cannot be followed up — there is no agent to
// wake. It must still auto-resolve, otherwise an abandoned ticket with no
// conversation would stay open forever.
func TestSweepOnce(t *testing.T) {
	l := newLayout(t)
	now := time.Now()
	hour := int64(3600)
	week := int64(7 * 24 * 3600)

	// Ticket config lives on the project.
	p, err := project.Load(l, "p1")
	if err != nil {
		t.Fatal(err)
	}
	p.Meta.Ticket = cfg(hour, week)
	if err := project.SaveMeta(l, "p1", p.Meta); err != nil {
		t.Fatal(err)
	}

	mk := func(title, status string, updatedAgo time.Duration, sessions []string) Ticket {
		item, cerr := Create(l, CreateOptions{ProjectID: "p1", Title: title, Status: status, Sessions: sessions})
		if cerr != nil {
			t.Fatal(cerr)
		}
		item.UpdatedAt = now.Add(-updatedAgo)
		if serr := SaveKeepingTimestamp(l, item); serr != nil {
			t.Fatal(serr)
		}
		return item
	}
	stale := mk("stale", StatusOpen, 2*time.Hour, []string{"sess-stale"})
	dead := mk("dead", StatusWaiting, 8*24*time.Hour, []string{"sess-dead"})
	fresh := mk("fresh", StatusOpen, time.Minute, []string{"sess-fresh"})
	orphan := mk("no sessions", StatusOpen, 2*time.Hour, nil)

	var sent []string
	d := Deps{
		Layout:       l,
		ListProjects: func() ([]project.Project, error) { return []project.Project{p}, nil },
		SendFollowup: func(sessionID, text string) error {
			sent = append(sent, sessionID)
			return nil
		},
	}

	sweepOnce(context.Background(), d, now)

	if len(sent) != 1 || sent[0] != "sess-stale" {
		t.Fatalf("followup sent to %v, want [sess-stale]", sent)
	}
	if got, _ := Load(l, "p1", dead.ID); got.Status != StatusDone {
		t.Fatalf("dead ticket not auto-resolved: %+v", got)
	}
	if got, _ := Load(l, "p1", stale.ID); got.LastFollowupAt.IsZero() {
		t.Fatal("LastFollowupAt not stamped after followup")
	}
	if got, _ := Load(l, "p1", fresh.ID); got.Status != StatusOpen || !got.LastFollowupAt.IsZero() {
		t.Fatalf("fresh ticket touched: %+v", got)
	}
	// No session to wake, so nothing is sent — but the ticket is not dead
	// yet either, so it stays open.
	if got, _ := Load(l, "p1", orphan.ID); got.Status != StatusOpen {
		t.Fatalf("orphan ticket status = %q, want open", got.Status)
	}

	// Second pass right after: the followup guard holds.
	sent = nil
	sweepOnce(context.Background(), d, now.Add(time.Minute))
	if len(sent) != 0 {
		t.Fatalf("followup re-sent inside the guard window: %v", sent)
	}
}

func TestSweepSkipsProjectsWithTicketModeOff(t *testing.T) {
	l := newLayout(t)
	now := time.Now()
	p, _ := project.Load(l, "p1")
	// Enabled false, timers set: must still be skipped.
	p.Meta.Ticket = project.TicketConfig{FollowupAfterSec: 3600, AutoResolveAfterSec: 60}
	if err := project.SaveMeta(l, "p1", p.Meta); err != nil {
		t.Fatal(err)
	}
	item, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "x", Sessions: []string{"s1"}})
	item.UpdatedAt = now.Add(-100 * time.Hour)
	if err := SaveKeepingTimestamp(l, item); err != nil {
		t.Fatal(err)
	}

	var sent []string
	sweepOnce(context.Background(), Deps{
		Layout:       l,
		ListProjects: func() ([]project.Project, error) { return []project.Project{p}, nil },
		SendFollowup: func(string, string) error { sent = append(sent, "x"); return nil },
	}, now)

	if len(sent) != 0 {
		t.Fatalf("followup sent for a ticket-mode-off project: %v", sent)
	}
	if got, _ := Load(l, "p1", item.ID); got.Status != StatusOpen {
		t.Fatalf("ticket auto-resolved in a ticket-mode-off project: %+v", got)
	}
}
