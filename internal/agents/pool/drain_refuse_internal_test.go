package pool

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// B5: a process that handed over starts no turn wick sends itself; a
// person's channel message and a turn already running here still go.
func TestDrainingProcessRefusesInternalTurns(t *testing.T) {
	orig := drainingNow
	t.Cleanup(func() { drainingNow = orig })
	p := &Pool{}
	idle := &runEntry{state: state.New(nil)}
	idle.state.Apply(event.AgentEvent{Type: event.Done}) // turn over: lifecycle idle
	working := &runEntry{state: state.New(nil)} // spawning counts as running

	drainingNow = func() bool { return false }
	if p.refuseWhileDrainingLocked(nil, false, "team") {
		t.Fatal("refused while not draining")
	}
	drainingNow = func() bool { return true }
	for _, src := range []string{"team", "subagent", "schedule", "cli", "agent", "workflow"} {
		if !p.refuseWhileDrainingLocked(nil, false, src) {
			t.Errorf("%s: a new turn was let in while draining", src)
		}
		if !p.refuseWhileDrainingLocked(idle, true, src) {
			t.Errorf("%s: an idle leftover subprocess was woken while draining", src)
		}
		if p.refuseWhileDrainingLocked(working, true, src) {
			t.Errorf("%s: refused into a turn already running here", src)
		}
	}
	for _, src := range []string{"slack", "telegram", "ui", ""} {
		if p.refuseWhileDrainingLocked(nil, false, src) {
			t.Errorf("%s: channel intake refused while draining", src)
		}
	}
}

// Send returns ErrHandedOver for it, before anything is spawned.
func TestSendWhileDrainingReturnsErrHandedOver(t *testing.T) {
	orig := drainingNow
	t.Cleanup(func() { drainingNow = orig })
	drainingNow = func() bool { return true }
	p, _ := newPool(t, 1, &scriptedSpawner{})
	if err := p.Send(context.Background(), "S", "default", "team", "user", "hi"); !errors.Is(err, ErrHandedOver) {
		t.Fatalf("err = %v", err)
	}
}
