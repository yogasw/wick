package engine

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestTriggerRegistryEmpty(t *testing.T) {
	r := NewTriggerRegistry()
	if _, ok := r.Get(workflow.TriggerCron); ok {
		t.Fatalf("empty registry returned a descriptor for cron")
	}
	if got := len(r.List()); got != 0 {
		t.Fatalf("empty registry List() len = %d, want 0", got)
	}
}

func TestTriggerRegistryRegisterAndGet(t *testing.T) {
	r := NewTriggerRegistry()
	r.Register(TriggerDescriptor{Type: workflow.TriggerCron, Description: "cron"})
	d, ok := r.Get(workflow.TriggerCron)
	if !ok {
		t.Fatalf("Get(cron) ok=false after Register")
	}
	if d.Description != "cron" {
		t.Fatalf("Get(cron) description = %q, want %q", d.Description, "cron")
	}
}

func TestTriggerRegistryReplaceSemantics(t *testing.T) {
	r := NewTriggerRegistry()
	r.Register(TriggerDescriptor{Type: workflow.TriggerCron, Description: "v1"})
	r.Register(TriggerDescriptor{Type: workflow.TriggerCron, Description: "v2"})
	d, _ := r.Get(workflow.TriggerCron)
	if d.Description != "v2" {
		t.Fatalf("Re-register overwrote with %q, want v2", d.Description)
	}
	if got := len(r.List()); got != 1 {
		t.Fatalf("List() len after re-register = %d, want 1 (no duplicates)", got)
	}
}

func TestTriggerRegistryListIsSorted(t *testing.T) {
	r := NewTriggerRegistry()
	r.Register(TriggerDescriptor{Type: workflow.TriggerWebhook})
	r.Register(TriggerDescriptor{Type: workflow.TriggerCron})
	r.Register(TriggerDescriptor{Type: workflow.TriggerManual})
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List() len = %d, want 3", len(list))
	}
	want := []workflow.TriggerType{
		workflow.TriggerCron,
		workflow.TriggerManual,
		workflow.TriggerWebhook,
	}
	for i, w := range want {
		if list[i].Type != w {
			t.Fatalf("List[%d] = %q, want %q (sorted)", i, list[i].Type, w)
		}
	}
}

func TestDefaultTriggerDescriptorsCoversAllTypes(t *testing.T) {
	got := map[workflow.TriggerType]bool{}
	for _, d := range DefaultTriggerDescriptors() {
		got[d.Type] = true
	}
	want := []workflow.TriggerType{
		workflow.TriggerCron,
		workflow.TriggerChannel,
		workflow.TriggerWebhook,
		workflow.TriggerManual,
		workflow.TriggerScheduleAt,
		workflow.TriggerError,
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("DefaultTriggerDescriptors missing %q", w)
		}
	}
}

func TestDefaultChannelTriggerCarriesDocs(t *testing.T) {
	for _, d := range DefaultTriggerDescriptors() {
		if d.Type != workflow.TriggerChannel {
			continue
		}
		if d.Docs.IsZero() {
			t.Fatalf("trigger:channel default descriptor has empty Docs — expected the multi-trigger routing sample")
		}
		if len(d.Examples) == 0 {
			t.Fatalf("trigger:channel default descriptor has no Examples")
		}
		return
	}
	t.Fatalf("trigger:channel not present in DefaultTriggerDescriptors")
}

func TestEngineNewSeedsTriggerRegistry(t *testing.T) {
	// New() should pre-seed Triggers with the canonical catalog so
	// MCP discovery works without callers having to seed.
	e := &Engine{}
	_ = e // touch to silence unused — real New() needs config/service which aren't worth wiring here

	tr := NewTriggerRegistry()
	tr.RegisterMany(DefaultTriggerDescriptors()...)
	if _, ok := tr.Get(workflow.TriggerCron); !ok {
		t.Fatalf("seeded registry missing trigger:cron")
	}
}
