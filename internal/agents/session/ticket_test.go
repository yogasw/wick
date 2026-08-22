package session

import (
	"context"
	"testing"
	"time"
)

func TestTicketRoundTrip(t *testing.T) {
	layout := newLayout(t)
	s, err := Create(context.Background(), layout, CreateOptions{ID: "tkt1"})
	if err != nil {
		t.Fatal(err)
	}

	// Fresh session has no ticket.
	if s.Meta.Ticket != nil {
		t.Fatalf("new session should have nil Ticket, got %+v", s.Meta.Ticket)
	}

	now := time.Now().UTC().Truncate(time.Second)
	s.Meta.Ticket = &Ticket{
		Status:    TicketInProgress,
		Assignee:  "user-1",
		Fields:    map[string]string{"type": "incident", "priority": "high"},
		UpdatedAt: now,
	}
	if err := SaveMeta(layout, "tkt1", s.Meta); err != nil {
		t.Fatal(err)
	}

	got, err := Load(layout, "tkt1")
	if err != nil {
		t.Fatal(err)
	}
	tk := got.Meta.Ticket
	if tk == nil {
		t.Fatal("ticket lost on round-trip")
	}
	if tk.Status != TicketInProgress || tk.Assignee != "user-1" {
		t.Fatalf("ticket mismatch: %+v", tk)
	}
	if tk.Fields["priority"] != "high" {
		t.Fatalf("fields mismatch: %+v", tk.Fields)
	}
	if !tk.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at mismatch: got %v want %v", tk.UpdatedAt, now)
	}
}

func TestValidTicketStatus(t *testing.T) {
	for _, ok := range []string{TicketOpen, TicketInProgress, TicketWaiting, TicketDone} {
		if !ValidTicketStatus(ok) {
			t.Errorf("ValidTicketStatus(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "closed", "OPEN", "in-progress"} {
		if ValidTicketStatus(bad) {
			t.Errorf("ValidTicketStatus(%q) = true, want false", bad)
		}
	}
}
