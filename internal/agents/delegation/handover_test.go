package delegation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/upgrade"
)

/* ── sub-agents still running in a draining predecessor ───────────────── */

// After a reload the successor shares the delegation table but not the old
// pool. A sub-agent the old process is still running is absent from this
// pool, and the sweep used to close it "(no output)" three minutes in.
func TestSweeperSparesAChildHeldByADrainingPredecessor(t *testing.T) {
	s, r, _ := newService(t)
	s.Deliver = &recordingDeliverer{}
	s.AgentAlive = func(string, string) bool { return false } // not in THIS pool
	s.DrainDir = t.TempDir()
	row := seedRunning(t, r, "a1")
	ageRow(t, r, row.ID)

	// The parent process stands in for the live predecessor.
	upgrade.PublishDrainState(s.DrainDir, os.Getppid(), time.Now(), nil, row.ChildSessionID)

	(&DelegationSweeper{Svc: s, Every: time.Minute}).Pass(context.Background())

	got, _ := r.Get(context.Background(), "a1")
	if got.Status != entity.DelegationRunning {
		t.Fatalf("status = %q, want running — the predecessor is still running it", got.Status)
	}
}

// The spare is only as good as the record behind it: a stale record or a
// dead pid means nobody is running the child, and the run is closed.
func TestSweeperClosesWhenThePredecessorRecordIsStaleOrDead(t *testing.T) {
	cases := map[string]func(dir, sess string){
		"stale": func(dir, sess string) {
			upgrade.PublishDrainStateAt(dir, os.Getppid(), time.Now(), nil, time.Now().Add(-time.Minute), sess)
		},
		"dead pid": func(dir, sess string) {
			upgrade.PublishDrainState(dir, 999999, time.Now(), nil, sess)
		},
	}
	for name, publish := range cases {
		t.Run(name, func(t *testing.T) {
			s, r, _ := newService(t)
			s.Deliver = &recordingDeliverer{}
			s.AgentAlive = func(string, string) bool { return false }
			s.DrainDir = t.TempDir()
			row := seedRunning(t, r, "a1")
			ageRow(t, r, row.ID)
			publish(s.DrainDir, row.ChildSessionID)

			(&DelegationSweeper{Svc: s, Every: time.Minute}).Pass(context.Background())

			got, _ := r.Get(context.Background(), "a1")
			if got.Status != entity.DelegationDone {
				t.Fatalf("status = %q, want done — no live process holds the child", got.Status)
			}
		})
	}
}

// Continue on a row closed under a still-running child started a second
// process in the same session: two writers in one tree. It must refuse.
func TestContinueRefusesWhileTheChildIsStillBusy(t *testing.T) {
	cases := map[string]func(s *Service, sess string){
		"this process": func(s *Service, sess string) {
			s.AgentBusy = func(child, _ string) bool { return child == sess }
		},
		"draining predecessor": func(s *Service, sess string) {
			s.DrainDir = t.TempDir()
			upgrade.PublishDrainState(s.DrainDir, os.Getppid(), time.Now(), nil, sess)
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			s, r, _ := newService(t)
			seedProfile(t, r, "researcher")
			row := seedDelegation(t, r, "d-done", "root-1", entity.DelegationDone, 1)
			arrange(s, row.ChildSessionID)

			_, err := s.Continue(context.Background(), ContinueRequest{
				DelegationID: row.ID, Task: "keep going", ActorID: row.TriggeredBy,
			})
			if !errors.Is(err, ErrNotContinuable) {
				t.Fatalf("err = %v, want ErrNotContinuable", err)
			}
			if !strings.Contains(err.Error(), "still running") {
				t.Fatalf("refusal %q does not say why", err)
			}
		})
	}
}

// The child the sweep closed empty finishes later with a real answer. That
// answer is kept instead of being dropped with "row already terminal".
func TestLateResultFillsARowClosedEmpty(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")
	if ok, err := r.FinishGuarded(context.Background(), row.ID, entity.DelegationRunning,
		entity.DelegationDone, "", "", 0); err != nil || !ok {
		t.Fatalf("sweep close: ok=%v err=%v", ok, err)
	}

	s.finish(context.Background(), row, entity.DelegationRunning, entity.DelegationDone, "the real report", "", 7)

	got, _ := r.Get(context.Background(), "a1")
	if got.Result != "the real report" || got.TurnsUsed != 7 {
		t.Fatalf("result=%q turns=%d, want the late report kept", got.Result, got.TurnsUsed)
	}
}

// Only an EMPTY close is filled: a result already on the row stays.
func TestLateResultNeverOverwritesAnExistingResult(t *testing.T) {
	s, r, _ := newService(t)
	row := seedRunning(t, r, "a1")
	if ok, err := r.FinishGuarded(context.Background(), row.ID, entity.DelegationRunning,
		entity.DelegationDone, "first answer", "", 2); err != nil || !ok {
		t.Fatalf("close: ok=%v err=%v", ok, err)
	}

	s.finish(context.Background(), row, entity.DelegationRunning, entity.DelegationDone, "late answer", "", 7)

	got, _ := r.Get(context.Background(), "a1")
	if got.Result != "first answer" {
		t.Fatalf("result = %q, want the existing result untouched", got.Result)
	}
}
