package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/job"
)

// newStuckSvc wires a Service on an in-memory DB with one instant job
// ("quick") and one ctx-honouring blocker ("sleepy") whose Run signals
// start and returns only when its context is cancelled.
func newStuckSvc(t *testing.T) (*Service, chan struct{}, chan error) {
	t.Helper()
	db := newJobsAPIDB(t)
	svc := NewServiceFromDB(db)
	started := make(chan struct{}, 8)
	stopped := make(chan error, 8)
	mods := []job.Module{
		{
			Meta: job.Meta{Key: "quick", Name: "Quick", DefaultCron: ""},
			Run:  func(context.Context) (string, error) { return "ok", nil },
		},
		{
			Meta: job.Meta{Key: "sleepy", Name: "Sleepy", DefaultCron: ""},
			Run: func(ctx context.Context) (string, error) {
				started <- struct{}{}
				<-ctx.Done()
				stopped <- ctx.Err()
				return "", ctx.Err()
			},
		},
	}
	if err := svc.Bootstrap(context.Background(), mods); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return svc, started, stopped
}

func markRunning(t *testing.T, svc *Service, key string) *entity.Job {
	t.Helper()
	j, err := svc.GetJob(context.Background(), key)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if err := svc.repo.SetStatus(context.Background(), j.ID, entity.JobStatusRunning); err != nil {
		t.Fatalf("set running: %v", err)
	}
	j.LastStatus = entity.JobStatusRunning
	return j
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The bug from production: last_status="running" with no open run row at
// all (crash between FinishRun and SetStatus, or purged history). The old
// sweep returned early unless it found an open run, so the row stuck
// forever. The sweep must now flip it to idle.
func TestResetStuckClearsOrphanedRunningStatus(t *testing.T) {
	svc, _, _ := newStuckSvc(t)
	j := markRunning(t, svc, "quick")

	reset, err := svc.repo.ResetStuckForJob(context.Background(), j)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !reset {
		t.Fatal("sweep did not reset the orphaned running status")
	}
	got, _ := svc.GetJob(context.Background(), "quick")
	if got.LastStatus != entity.JobStatusIdle {
		t.Fatalf("last_status = %q, want idle", got.LastStatus)
	}
}

// A genuinely running job (open run row younger than the timeout) must NOT
// be flipped by the sweep.
func TestResetStuckKeepsFreshRun(t *testing.T) {
	svc, started, _ := newStuckSvc(t)
	if _, err := svc.RunManual(context.Background(), "sleepy", "u1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	<-started
	j, _ := svc.GetJob(context.Background(), "sleepy")
	if reset, err := svc.repo.ResetStuckForJob(context.Background(), j); err != nil || reset {
		t.Fatalf("sweep on live run: reset=%v err=%v, want no-op", reset, err)
	}
	got, _ := svc.GetJob(context.Background(), "sleepy")
	if got.LastStatus != entity.JobStatusRunning {
		t.Fatalf("last_status = %q, want running", got.LastStatus)
	}
	if err := svc.CancelJob(context.Background(), "sleepy"); err != nil {
		t.Fatalf("cleanup cancel: %v", err)
	}
}

// Run Now on a stale "running" row (nothing actually executing) must
// self-repair and start the run instead of refusing forever.
func TestRunManualRecoversStaleRunningStatus(t *testing.T) {
	svc, _, _ := newStuckSvc(t)
	markRunning(t, svc, "quick")

	runID, err := svc.RunManual(context.Background(), "quick", "u1")
	if err != nil {
		t.Fatalf("RunManual on stale row: %v, want it to reconcile and run", err)
	}
	waitFor(t, "run to finish", func() bool {
		run, err := svc.GetRun(context.Background(), runID)
		return err == nil && run.Status == entity.RunStatusSuccess
	})
}

// Run Now while the job is genuinely running (in-process) must still refuse.
func TestRunManualRefusesLiveRun(t *testing.T) {
	svc, started, _ := newStuckSvc(t)
	if _, err := svc.RunManual(context.Background(), "sleepy", "u1"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	<-started
	if _, err := svc.RunManual(context.Background(), "sleepy", "u1"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second run err = %v, want already running", err)
	}
	if err := svc.CancelJob(context.Background(), "sleepy"); err != nil {
		t.Fatalf("cleanup cancel: %v", err)
	}
}

// Cancel must actually stop the run (context cancel), close the run row as
// cancelled, flip the job to idle, and leave the job immediately runnable.
// The late finalize from the cancelled goroutine must not rewrite the row.
func TestCancelJobStopsRunAndKeepsCancelledStatus(t *testing.T) {
	svc, started, stopped := newStuckSvc(t)
	runID, err := svc.RunManual(context.Background(), "sleepy", "u1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	<-started

	if err := svc.CancelJob(context.Background(), "sleepy"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cerr := <-stopped; cerr != context.Canceled {
		t.Fatalf("run ctx err = %v, want context.Canceled", cerr)
	}

	j, _ := svc.GetJob(context.Background(), "sleepy")
	if j.LastStatus != entity.JobStatusIdle {
		t.Fatalf("last_status = %q, want idle", j.LastStatus)
	}
	run, _ := svc.GetRun(context.Background(), runID)
	if run.Status != entity.RunStatusCancelled {
		t.Fatalf("run status = %q, want cancelled", run.Status)
	}

	// Give the goroutine's finalize a beat, then confirm it did not
	// overwrite "cancelled" with the RunFunc's error.
	time.Sleep(100 * time.Millisecond)
	run, _ = svc.GetRun(context.Background(), runID)
	if run.Status != entity.RunStatusCancelled {
		t.Fatalf("run status after finalize = %q, want cancelled to stick", run.Status)
	}

	// Job is immediately runnable again.
	if _, err := svc.RunManual(context.Background(), "sleepy", "u1"); err != nil {
		t.Fatalf("re-run after cancel: %v", err)
	}
	<-started
	if err := svc.CancelJob(context.Background(), "sleepy"); err != nil {
		t.Fatalf("cleanup cancel: %v", err)
	}
	<-stopped
}

// Cancel on a stale "running" row repairs it instead of erroring — the
// button doubles as the operator's unstick action.
func TestCancelJobRepairsStaleRow(t *testing.T) {
	svc, _, _ := newStuckSvc(t)
	markRunning(t, svc, "quick")

	if err := svc.CancelJob(context.Background(), "quick"); err != nil {
		t.Fatalf("cancel stale: %v", err)
	}
	j, _ := svc.GetJob(context.Background(), "quick")
	if j.LastStatus != entity.JobStatusIdle {
		t.Fatalf("last_status = %q, want idle", j.LastStatus)
	}
}

// Cancel on a job idle everywhere still errors.
func TestCancelJobIdleErrors(t *testing.T) {
	svc, _, _ := newStuckSvc(t)
	if err := svc.CancelJob(context.Background(), "quick"); err == nil {
		t.Fatal("cancel idle job: want error, got nil")
	}
}

// Disabling a running job stops the run: ctx cancelled, run row closed as
// cancelled, job idle.
func TestDisableCancelsRunningJob(t *testing.T) {
	svc, started, stopped := newStuckSvc(t)
	runID, err := svc.RunManual(context.Background(), "sleepy", "u1")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	<-started

	if err := svc.UpdateSchedule(context.Background(), "sleepy", "", false, 0, 30); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if cerr := <-stopped; cerr != context.Canceled {
		t.Fatalf("run ctx err = %v, want context.Canceled", cerr)
	}
	run, _ := svc.GetRun(context.Background(), runID)
	if run.Status != entity.RunStatusCancelled {
		t.Fatalf("run status = %q, want cancelled", run.Status)
	}
	j, _ := svc.GetJob(context.Background(), "sleepy")
	if j.LastStatus != entity.JobStatusIdle || j.Enabled {
		t.Fatalf("job = {status:%q enabled:%v}, want idle+disabled", j.LastStatus, j.Enabled)
	}
}

// Bootstrap must sweep DISABLED jobs too: a stuck row on a disabled job
// still blocks Run Now, so it is not cosmetic.
func TestBootstrapSweepsDisabledJobs(t *testing.T) {
	db := newJobsAPIDB(t)
	svc := NewServiceFromDB(db)
	mods := []job.Module{{
		Meta: job.Meta{Key: "quick", Name: "Quick"},
		Run:  func(context.Context) (string, error) { return "ok", nil },
	}}
	if err := svc.Bootstrap(context.Background(), mods); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	markRunning(t, svc, "quick") // job is disabled by default (Enabled=false)

	svc2 := NewServiceFromDB(db)
	if err := svc2.Bootstrap(context.Background(), mods); err != nil {
		t.Fatalf("re-bootstrap: %v", err)
	}
	j, _ := svc2.GetJob(context.Background(), "quick")
	if j.LastStatus != entity.JobStatusIdle {
		t.Fatalf("last_status after bootstrap = %q, want idle", j.LastStatus)
	}
}
