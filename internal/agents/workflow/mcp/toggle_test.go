package mcp

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// toggleService records what Toggle did and answers Load from the same
// state, so a toggle that never reached the record is visible.
type toggleService struct {
	*stubService
	// persisted is the authoritative enabled flag — the workflow row, not
	// the draft body.
	persisted bool
	// deaf drops the write, reproducing the canvas path that edited the
	// draft and left the row alone.
	deaf  bool
	calls int
}

func (s *toggleService) Toggle(_ string, enabled bool) error {
	s.calls++
	if s.deaf {
		return nil
	}
	s.persisted = enabled
	return nil
}

func (s *toggleService) Load(string) (workflow.Workflow, error) {
	w := s.stubService.wf
	w.Enabled = s.persisted
	return w, nil
}

// TestToggleFlipsThePersistedRecord: the agent-facing toggle has to move
// the record every other reader consults, not the draft body.
func TestToggleFlipsThePersistedRecord(t *testing.T) {
	svc := &toggleService{stubService: newStubService(stubWorkflow())}
	ops := &Ops{Service: svc}

	w, err := ops.Toggle("wf1", true)
	if err != nil {
		t.Fatal(err)
	}
	if !svc.persisted {
		t.Fatal("toggle did not reach the persisted record")
	}
	if !w.Enabled {
		t.Fatalf("returned workflow says enabled=%v", w.Enabled)
	}

	if _, err := ops.Toggle("wf1", false); err != nil {
		t.Fatal(err)
	}
	if svc.persisted {
		t.Fatal("disable did not reach the persisted record")
	}
}

// TestToggleReportsAWriteThatDidNotStick is the regression that started
// this: a toggle that changes nothing must not answer "ok". Reported as
// a permissions problem for weeks because the call looked successful.
func TestToggleReportsAWriteThatDidNotStick(t *testing.T) {
	svc := &toggleService{stubService: newStubService(stubWorkflow()), deaf: true}
	ops := &Ops{Service: svc}

	_, err := ops.Toggle("wf1", true)
	if err == nil {
		t.Fatal("a toggle that did not persist reported success")
	}
	if svc.calls != 1 {
		t.Fatalf("Toggle called %d times, want 1", svc.calls)
	}
}
