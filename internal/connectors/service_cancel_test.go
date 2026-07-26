package connectors

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// blockingModule registers a "block" op that waits on its context until
// cancelled — a faithful stand-in for a plugin gRPC call that honors ctx
// cancellation (stream.Recv unblocks on cancel). A "started" channel signals
// when the op is actually running so the test can cancel at the right moment.
func blockingModule(started chan<- struct{}) connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "blk", Name: "Block", Description: "test", Fixed: true},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("block", "Block", "blocks until ctx cancelled",
					struct{}{},
					func(c *connector.Ctx) (any, error) {
						select {
						case started <- struct{}{}:
						default:
						}
						<-c.Context().Done()
						return nil, c.Context().Err()
					}, wickdocs.Docs{},
				),
			),
		},
	}
}

func newSvcWithBlocking(t *testing.T, started chan<- struct{}) (*Service, string) {
	t.Helper()
	db := newSQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{blockingModule(started)}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rows, _ := svc.List(context.Background())
	if len(rows) == 0 {
		t.Fatal("no connector row")
	}
	return svc, rows[0].ID
}

// TestCancelSessionAbortsInflightOp is the core guarantee: closing a session
// aborts every op running under it, and the op finalizes as "cancelled" instead
// of hanging in "running" forever.
func TestCancelSessionAbortsInflightOp(t *testing.T) {
	started := make(chan struct{}, 1)
	svc, connID := newSvcWithBlocking(t, started)

	const sid = "sess-abc"
	type execOut struct {
		res *ExecuteResult
		err error
	}
	out := make(chan execOut, 1)
	go func() {
		res, err := svc.Execute(context.Background(), ExecuteParams{
			ConnectorID:  connID,
			OperationKey: "block",
			Input:        map[string]string{"session_id": sid},
			Source:       entity.ConnectorRunSourceTest,
			UserID:       "u",
		})
		out <- execOut{res, err}
	}()

	// Wait until the op is actually executing before cancelling.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("op never started")
	}

	if n := svc.CancelSession(sid); n != 1 {
		t.Fatalf("CancelSession returned %d, want 1", n)
	}

	select {
	case o := <-out:
		if o.res == nil {
			t.Fatalf("nil result, err=%v", o.err)
		}
		if o.res.Status != entity.ConnectorRunStatusCancelled {
			t.Fatalf("status = %q, want %q", o.res.Status, entity.ConnectorRunStatusCancelled)
		}
		// The agent-facing result must explicitly say it was cancelled by the user
		// so the model doesn't silently retry or assume a result.
		if !strings.Contains(strings.ToLower(o.res.ErrorMessage), "cancelled by the user") {
			t.Fatalf("cancel result should tell the agent it was user-cancelled, got %q", o.res.ErrorMessage)
		}
		if o.err == nil {
			t.Fatal("cancelled op should return a non-nil error to the caller")
		}
		// The run row must be finalized (not left "running").
		row, err := svc.repo.GetRun(context.Background(), o.res.RunID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if row.Status != entity.ConnectorRunStatusCancelled {
			t.Fatalf("persisted status = %q, want %q", row.Status, entity.ConnectorRunStatusCancelled)
		}
		if row.EndedAt == nil {
			t.Fatal("EndedAt not set — run was not finalized")
		}
		if row.SessionID != sid {
			t.Fatalf("SessionID = %q, want %q", row.SessionID, sid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("op did not return after CancelSession — cancellation not propagated")
	}
}

// TestCancelSessionUnknownIsNoop: cancelling a session with no in-flight ops
// returns 0 and doesn't panic.
func TestCancelSessionUnknownIsNoop(t *testing.T) {
	started := make(chan struct{}, 1)
	svc, _ := newSvcWithBlocking(t, started)
	if n := svc.CancelSession("nobody"); n != 0 {
		t.Fatalf("CancelSession(unknown) = %d, want 0", n)
	}
	if n := svc.CancelSession(""); n != 0 {
		t.Fatalf("CancelSession(empty) = %d, want 0", n)
	}
}

// TestResetStuckRunsFinalizesOldRunning: a row left "running" past the cutoff is
// reclaimed as "cancelled" by the boot reaper; a fresh one is left alone.
func TestResetStuckRunsFinalizesOldRunning(t *testing.T) {
	db := newSQLite(t)
	svc := NewServiceFromDB(db)

	old := &entity.ConnectorRun{
		ID:          "run-old",
		ConnectorID: "c1",
		Status:      entity.ConnectorRunStatusRunning,
		Source:      entity.ConnectorRunSourceTest,
		StartedAt:   time.Now().Add(-1 * time.Hour),
	}
	fresh := &entity.ConnectorRun{
		ID:          "run-fresh",
		ConnectorID: "c1",
		Status:      entity.ConnectorRunStatusRunning,
		Source:      entity.ConnectorRunSourceTest,
		StartedAt:   time.Now(),
	}
	if err := svc.repo.CreateRun(context.Background(), old); err != nil {
		t.Fatalf("create old: %v", err)
	}
	if err := svc.repo.CreateRun(context.Background(), fresh); err != nil {
		t.Fatalf("create fresh: %v", err)
	}

	n, err := svc.repo.ResetStuckRuns(context.Background(), time.Now().Add(-2*time.Minute))
	if err != nil {
		t.Fatalf("ResetStuckRuns: %v", err)
	}
	if n != 1 {
		t.Fatalf("reset %d rows, want 1 (only the old one)", n)
	}

	gotOld, _ := svc.repo.GetRun(context.Background(), "run-old")
	if gotOld.Status != entity.ConnectorRunStatusCancelled {
		t.Fatalf("old status = %q, want cancelled", gotOld.Status)
	}
	if gotOld.EndedAt == nil {
		t.Fatal("old run not finalized (EndedAt nil)")
	}
	gotFresh, _ := svc.repo.GetRun(context.Background(), "run-fresh")
	if gotFresh.Status != entity.ConnectorRunStatusRunning {
		t.Fatalf("fresh status = %q, want still running", gotFresh.Status)
	}
}
