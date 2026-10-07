package setup

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/state"
)

var retentionNow = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// seedRun writes a finished run (state.json + index row) that started
// `age` before retentionNow. indexed=false leaves it out of the index,
// like a run whose index append failed.
func seedRun(t *testing.T, ss *state.FileStore, wf, rid, status string, age time.Duration, indexed bool) {
	t.Helper()
	start := retentionNow.Add(-age)
	end := start.Add(time.Second)
	st := workflow.RunState{RunID: rid, WorkflowID: wf, Status: status, StartedAt: start, EndedAt: &end}
	if status == workflow.StatusRunning || status == workflow.StatusPaused {
		st.EndedAt = nil
	}
	if err := ss.Save(wf, rid, st); err != nil {
		t.Fatal(err)
	}
	if indexed && st.EndedAt != nil {
		if err := ss.IndexAppend(wf, state.IndexEntry{ID: rid, Status: status, StartedAt: start, EndedAt: &end}); err != nil {
			t.Fatal(err)
		}
	}
}

func runExists(layout config.Layout, wf, rid string) bool {
	_, err := os.Stat(layout.WorkflowRunDir(wf, rid))
	return err == nil
}

func indexIDs(t *testing.T, ss *state.FileStore, wf string) map[string]bool {
	t.Helper()
	rows, err := ss.IndexAll(wf)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.ID] = true
	}
	return out
}

func opts() CleanupOptions {
	return CleanupOptions{Now: func() time.Time { return retentionNow }}
}

// 60 young runs written in random-looking id order: the 50 newest by
// StartedAt survive, the 10 oldest go, and the index follows.
func TestCleanupKeepMaxByStartedAt(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	ss := state.New(layout)
	const wf = "wf1"
	// Ids sort opposite to age so folder-name order can't fake a pass.
	for i := 0; i < 60; i++ {
		rid := fmt.Sprintf("r%02d", 59-i) // r59 = newest
		seedRun(t, ss, wf, rid, workflow.StatusSuccess, time.Duration(i)*time.Minute, true)
	}
	n, err := CleanupWorkflowRuns(layout, wf, opts())
	if err != nil || n != 10 {
		t.Fatalf("removed=%d err=%v, want 10", n, err)
	}
	idx := indexIDs(t, ss, wf)
	for i := 0; i < 60; i++ {
		rid := fmt.Sprintf("r%02d", 59-i)
		keep := i < 50
		if runExists(layout, wf, rid) != keep || idx[rid] != keep {
			t.Fatalf("%s: dir=%v index=%v, want %v", rid, runExists(layout, wf, rid), idx[rid], keep)
		}
	}
}

// Inside the cap, anything older than 7 days goes — success and failed
// alike — while a run still in progress is never touched, however old.
func TestCleanupTTLAndActiveRuns(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	ss := state.New(layout)
	const wf = "wf1"
	day := 24 * time.Hour
	seedRun(t, ss, wf, "fresh", workflow.StatusSuccess, 6*day, true)
	seedRun(t, ss, wf, "old-ok", workflow.StatusSuccess, 8*day, true)
	seedRun(t, ss, wf, "old-fail", workflow.StatusFailed, 8*day, true)
	seedRun(t, ss, wf, "old-unindexed", workflow.StatusFailed, 9*day, false)
	seedRun(t, ss, wf, "running", workflow.StatusRunning, 30*day, false)
	seedRun(t, ss, wf, "paused", workflow.StatusPaused, 30*day, false)

	n, err := CleanupWorkflowRuns(layout, wf, opts())
	if err != nil || n != 3 {
		t.Fatalf("removed=%d err=%v, want 3", n, err)
	}
	for rid, keep := range map[string]bool{
		"fresh": true, "running": true, "paused": true,
		"old-ok": false, "old-fail": false, "old-unindexed": false,
	} {
		if runExists(layout, wf, rid) != keep {
			t.Fatalf("%s exists=%v, want %v", rid, !keep, keep)
		}
	}
	idx := indexIDs(t, ss, wf)
	if len(idx) != 1 || !idx["fresh"] {
		t.Fatalf("index = %v, want only fresh", idx)
	}
}

// Active runs don't take a slot: with KeepMax 2, two in-flight runs plus
// three finished ones keep the two newest finished.
func TestCleanupActiveRunsNotCounted(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	ss := state.New(layout)
	const wf = "wf1"
	seedRun(t, ss, wf, "a-running", workflow.StatusRunning, 0, false)
	seedRun(t, ss, wf, "b-paused", workflow.StatusPaused, time.Minute, false)
	seedRun(t, ss, wf, "c1", workflow.StatusSuccess, 2*time.Minute, true)
	seedRun(t, ss, wf, "c2", workflow.StatusSuccess, 3*time.Minute, true)
	seedRun(t, ss, wf, "c3", workflow.StatusSuccess, 4*time.Minute, true)

	o := opts()
	o.KeepMax = 2
	if n, err := CleanupWorkflowRuns(layout, wf, o); err != nil || n != 1 {
		t.Fatalf("removed=%d err=%v, want 1", n, err)
	}
	for rid, keep := range map[string]bool{"a-running": true, "b-paused": true, "c1": true, "c2": true, "c3": false} {
		if runExists(layout, wf, rid) != keep {
			t.Fatalf("%s exists=%v, want %v", rid, !keep, keep)
		}
	}
}

// Index rows whose folder is already gone are dropped, and shards left
// empty are deleted, so the Runs panel never lists a ghost.
func TestCleanupDropsGhostIndexRows(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	ss := state.New(layout)
	const wf = "wf1"
	seedRun(t, ss, wf, "ghost", workflow.StatusSuccess, time.Minute, true)
	if err := os.RemoveAll(layout.WorkflowRunDir(wf, "ghost")); err != nil {
		t.Fatal(err)
	}
	if _, err := CleanupWorkflowRuns(layout, wf, opts()); err != nil {
		t.Fatal(err)
	}
	if idx := indexIDs(t, ss, wf); len(idx) != 0 {
		t.Fatalf("index = %v, want empty", idx)
	}
	entries, _ := os.ReadDir(layout.WorkflowIndexDir(wf))
	for _, e := range entries {
		t.Fatalf("index shard %q left behind", e.Name())
	}
}

// Wiring retention at boot must not sweep: the predecessor may still own
// intake. Only the per-run hook is armed; StartRunSweep does the pass.
func TestStartRunRetentionDefersSweep(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	ss := state.New(layout)
	const wf = "wf1"
	seedRun(t, ss, wf, "old", workflow.StatusSuccess, 30*24*time.Hour, true)
	m := &Manager{Layout: layout, Engine: &engine.Engine{}}
	m.StartRunRetention(opts)
	if m.Engine.AfterRun == nil || m.retentionOpts == nil {
		t.Fatal("AfterRun hook / retention opts not wired")
	}
	if !runExists(layout, wf, "old") {
		t.Fatal("StartRunRetention swept at boot; sweep must wait for StartRunSweep")
	}
	m.Engine.AfterRun(wf)
	if runExists(layout, wf, "old") {
		t.Fatal("AfterRun hook did not prune the workflow")
	}
}
