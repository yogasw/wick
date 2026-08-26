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

// A board card carries only schema fields explicitly marked show_on_card.
// Values written outside the schema (the REST surface accepts any key) stay
// off the card entirely.
func TestCardFields(t *testing.T) {
	cfg := TicketConfig{Fields: []TicketField{
		{Key: "priority", Label: "Priority", Type: "select", ShowOnCard: true},
		{Key: "type", Label: "Type", Type: "select"}, // not marked
	}}
	values := map[string]string{
		"priority":   "high",
		"type":       "bug",
		"notion_url": "https://abc.com/p/x", // outside the schema
	}
	got := cfg.CardFields(values)
	if len(got) != 1 || got["priority"] != "high" {
		t.Fatalf("CardFields = %v, want only priority", got)
	}
	// Marked but empty value → no chip.
	if out := cfg.CardFields(map[string]string{"priority": ""}); out != nil {
		t.Fatalf("empty value should not appear, got %v", out)
	}
	// Nothing marked (the default) → the card shows no fields at all.
	none := TicketConfig{Fields: []TicketField{{Key: "priority", Type: "select"}}}
	if out := none.CardFields(values); out != nil {
		t.Fatalf("unmarked schema should yield nil, got %v", out)
	}
}

// ButtonByID resolves only buttons that can actually fire — an id match
// with a blank URL is a half-written editor row, not an action.
func TestButtonByID(t *testing.T) {
	cfg := TicketConfig{Integrations: TicketIntegrations{Buttons: []TicketButton{
		{ID: "btn_1", Label: "Sync", URL: "https://abc.com/hook"},
		{ID: "btn_2", Label: "Broken", URL: "  "},
	}}}
	if b, ok := cfg.ButtonByID("btn_1"); !ok || b.Label != "Sync" {
		t.Fatalf("btn_1 not resolved: %+v %v", b, ok)
	}
	if _, ok := cfg.ButtonByID("btn_2"); ok {
		t.Fatal("blank-URL button must not resolve")
	}
	if _, ok := cfg.ButtonByID("nope"); ok {
		t.Fatal("unknown id must not resolve")
	}
}

// Ticket mode is opt-in, and a project written before it existed must read
// as off rather than as a half-configured board.
func TestTicketConfigDefaultOff(t *testing.T) {
	layout := newTicketLayout(t)
	p, err := Create(layout, CreateOptions{ID: "p1", Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Meta.Ticket.Enabled {
		t.Fatal("ticket mode must default to off")
	}
	got, err := Load(layout, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Ticket.Enabled {
		t.Fatal("a loaded project must have ticket mode off")
	}
	if len(got.Meta.Ticket.AutoCreate) != 0 {
		t.Fatalf("no auto-create rules by default, got %+v", got.Meta.Ticket.AutoCreate)
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
		AutoCreate: []AutoCreateRule{
			{Origin: "slack", ChannelKind: ChannelKindDM, Enabled: false},
			{Origin: "slack", Enabled: true, Title: "[{origin}] {message}"},
		},
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
	if cfg.FollowupPrompt != "check and escalate" {
		t.Fatalf("prompt lost: %q", cfg.FollowupPrompt)
	}
	// Rule ORDER is load-bearing — the first match wins, so a disabled
	// narrow rule above a broad one is how an exception is written.
	if len(cfg.AutoCreate) != 2 {
		t.Fatalf("rules = %+v, want 2", cfg.AutoCreate)
	}
	if cfg.AutoCreate[0].ChannelKind != ChannelKindDM || cfg.AutoCreate[0].Enabled {
		t.Fatalf("the DM exception must stay first and disabled: %+v", cfg.AutoCreate[0])
	}
	if !cfg.AutoCreate[1].Enabled || cfg.AutoCreate[1].Title != "[{origin}] {message}" {
		t.Fatalf("second rule lost detail: %+v", cfg.AutoCreate[1])
	}
}

func TestDefaultTicketFields(t *testing.T) {
	f := DefaultTicketFields()
	if len(f) != 2 || f[0].Key != "type" || f[1].Key != "priority" {
		t.Fatalf("seed fields = %+v", f)
	}
	for _, def := range f {
		if def.Type != "select" || len(def.Options) == 0 {
			t.Fatalf("field %q should be a select with options: %+v", def.Key, def)
		}
		if def.Label == "" {
			t.Fatalf("field %q needs a label — the board renders it", def.Key)
		}
	}
}
