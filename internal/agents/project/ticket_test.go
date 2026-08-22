package project

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

func newTicketLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestTicketConfigDefaultOff(t *testing.T) {
	layout := newTicketLayout(t)
	p, err := Create(layout, CreateOptions{ID: "p1", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta.Ticket.Enabled {
		t.Fatal("ticket mode must default to off")
	}
	// Old meta.json (no ticket key) decodes to off too.
	got, err := Load(layout, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Ticket.Enabled {
		t.Fatal("loaded project must have ticket mode off")
	}
}

func TestTicketConfigRoundTrip(t *testing.T) {
	layout := newTicketLayout(t)
	p, err := Create(layout, CreateOptions{ID: "p2", Name: "two"})
	if err != nil {
		t.Fatal(err)
	}
	p.Meta.Ticket = TicketConfig{
		Enabled:             true,
		Fields:              DefaultTicketFields(),
		FollowupAfterSec:    3600,
		FollowupPrompt:      "check and escalate",
		AutoResolveAfterSec: 7 * 24 * 3600,
	}
	if err := SaveMeta(layout, "p2", p.Meta); err != nil {
		t.Fatal(err)
	}
	got, err := Load(layout, "p2")
	if err != nil {
		t.Fatal(err)
	}
	cfg := got.Meta.Ticket
	if !cfg.Enabled || cfg.FollowupAfterSec != 3600 || cfg.AutoResolveAfterSec != 7*24*3600 {
		t.Fatalf("config mismatch: %+v", cfg)
	}
	if len(cfg.Fields) != 2 || cfg.Fields[0].Key != "type" || cfg.Fields[1].Key != "priority" {
		t.Fatalf("default fields mismatch: %+v", cfg.Fields)
	}
	if cfg.Fields[0].Type != "select" || len(cfg.Fields[0].Options) == 0 {
		t.Fatalf("type field should be a select with options: %+v", cfg.Fields[0])
	}
}
