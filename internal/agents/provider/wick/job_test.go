package wick

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// job_test.go covers the async job model (job.go / job_tools.go): the
// manager lifecycle, the completion inject seam, the heartbeat keepalive,
// and the shell runner.

// waitJobStatus polls a job entry until it leaves running or the deadline
// passes.
func waitJobStatus(t *testing.T, je *jobEntry, within time.Duration) JobStatus {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		st, _, _, _, _ := je.snapshot()
		if st != jobRunning {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, _, _, _, _ := je.snapshot()
	return st
}

// captureInject records injected turns for assertions.
type injectSink struct {
	mu    sync.Mutex
	turns []string
}

func (s *injectSink) fn(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns = append(s.turns, text)
}

func (s *injectSink) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.turns...)
}

// okRunner is a trivial runner that writes a line and exits with the
// given code.
func okRunner(msg string, code int) jobRunner {
	return func(_ context.Context, out io.Writer) (int, error) {
		_, _ = io.WriteString(out, msg)
		return code, nil
	}
}

func TestJob_SucceedsAndInjects(t *testing.T) {
	sink := &injectSink{}
	m := newJobManager()
	m.setSeams(sink.fn, func() {})

	id, err := m.start(context.Background(), "shell", "echo hi", okRunner("hi\n", 0))
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	je, _ := m.get(id)
	if st := waitJobStatus(t, je, 3*time.Second); st != jobSucceeded {
		t.Fatalf("want succeeded, got %s", st)
	}
	// The completion inject must have fired with a [job-done] marker.
	waitFor(t, func() bool { return len(sink.get()) > 0 }, 2*time.Second)
	turns := sink.get()
	if len(turns) != 1 {
		t.Fatalf("want 1 injected turn, got %d: %v", len(turns), turns)
	}
	if !strings.Contains(turns[0], "[job-done") || !strings.Contains(turns[0], "ok=true") {
		t.Errorf("inject missing job-done/ok marker: %q", turns[0])
	}
	if !strings.Contains(turns[0], id) {
		t.Errorf("inject missing job id %q: %q", id, turns[0])
	}
}

func TestJob_NonZeroExitFails(t *testing.T) {
	sink := &injectSink{}
	m := newJobManager()
	m.setSeams(sink.fn, func() {})

	id, _ := m.start(context.Background(), "shell", "false", okRunner("", 3))
	je, _ := m.get(id)
	if st := waitJobStatus(t, je, 3*time.Second); st != jobFailed {
		t.Fatalf("want failed, got %s", st)
	}
	_, _, _, exit, _ := je.snapshot()
	if exit != 3 {
		t.Errorf("want exit 3, got %d", exit)
	}
	waitFor(t, func() bool { return len(sink.get()) > 0 }, 2*time.Second)
	if !strings.Contains(sink.get()[0], "ok=false") {
		t.Errorf("failed job should inject ok=false: %q", sink.get()[0])
	}
}

func TestJob_Cancel(t *testing.T) {
	sink := &injectSink{}
	m := newJobManager()
	m.setSeams(sink.fn, func() {})

	// A runner that blocks until ctx cancel.
	blocker := func(ctx context.Context, out io.Writer) (int, error) {
		<-ctx.Done()
		return -1, context.Canceled
	}
	id, _ := m.start(context.Background(), "shell", "sleep forever", blocker)
	je, _ := m.get(id)

	if err := m.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if st := waitJobStatus(t, je, 3*time.Second); st != jobCancelled {
		t.Fatalf("want cancelled, got %s", st)
	}
}

func TestJob_MaxCap(t *testing.T) {
	m := newJobManager()
	m.setSeams(func(string) {}, func() {})
	blocker := func(ctx context.Context, _ io.Writer) (int, error) { <-ctx.Done(); return -1, context.Canceled }
	for i := 0; i < maxJobs; i++ {
		if _, err := m.start(context.Background(), "shell", "block", blocker); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := m.start(context.Background(), "shell", "block", blocker); err == nil {
		t.Error("start past cap should error")
	}
	m.cancelAll()
}

func TestJob_CancelAllReaps(t *testing.T) {
	m := newJobManager()
	m.setSeams(func(string) {}, func() {})
	blocker := func(ctx context.Context, _ io.Writer) (int, error) { <-ctx.Done(); return -1, context.Canceled }
	id, _ := m.start(context.Background(), "shell", "block", blocker)
	je, _ := m.get(id)

	m.cancelAll()
	if st := waitJobStatus(t, je, 3*time.Second); st == jobRunning {
		t.Error("cancelAll left a job running")
	}
}

// ── shell runner ─────────────────────────────────────────────────────

func TestJob_ShellRunnerOutputAndExit(t *testing.T) {
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	sink := &injectSink{}
	m := newJobManager()
	m.setSeams(sink.fn, func() {})

	id, _ := m.start(context.Background(), "shell", "echo job-out && exit 0",
		shellJobRunner(t.TempDir(), "echo job-out && exit 0"))
	je, _ := m.get(id)
	if st := waitJobStatus(t, je, 10*time.Second); st != jobSucceeded {
		t.Fatalf("want succeeded, got %s", st)
	}
	_, out, _, _, _ := je.snapshot()
	if !strings.Contains(out, "job-out") {
		t.Errorf("shell runner output not captured: %q", out)
	}
}

func TestJob_ShellRunnerCancelKillsTree(t *testing.T) {
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	m := newJobManager()
	m.setSeams(func(string) {}, func() {})

	id, _ := m.start(context.Background(), "shell", "sleep 30", shellJobRunner(t.TempDir(), "sleep 30"))
	je, _ := m.get(id)

	start := time.Now()
	_ = m.cancel(id)
	st := waitJobStatus(t, je, 8*time.Second)
	if st != jobCancelled {
		t.Fatalf("want cancelled, got %s", st)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("cancel of sleep 30 took too long: %v (kill-tree not working)", time.Since(start))
	}
}

// ── tools wiring ─────────────────────────────────────────────────────

func TestJobTools_StartStatusLog(t *testing.T) {
	if _, err := resolveBash(); err != nil {
		t.Skipf("bash not available: %v", err)
	}
	m := newJobManager()
	m.setSeams(func(string) {}, func() {})
	tc := toolContext{Workspace: t.TempDir(), Jobs: m}

	start := jobStartTool(tc)
	res, isErr := start.handler(context.Background(), map[string]any{
		"tool":    "shell",
		"command": "echo tool-job",
	})
	if isErr {
		t.Fatalf("job_start errored: %s", res)
	}
	id := extractToken(t, res, "job_")

	// Poll status via the tool until done.
	statusTool := jobStatusTool(tc)
	logTool := jobLogTool(tc)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sres, _ := statusTool.handler(context.Background(), map[string]any{"job_id": id})
		if !strings.Contains(sres, "status: running") {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	lres, isErr := logTool.handler(context.Background(), map[string]any{"job_id": id})
	if isErr {
		t.Fatalf("job_log errored: %s", lres)
	}
	if !strings.Contains(lres, "tool-job") {
		t.Errorf("job_log missing output: %q", lres)
	}
}

func TestJobTools_NilManagerUnavailable(t *testing.T) {
	tc := toolContext{Workspace: t.TempDir()} // Jobs nil
	start := jobStartTool(tc)
	res, isErr := start.handler(context.Background(), map[string]any{"tool": "shell", "command": "echo x"})
	if !isErr || !strings.Contains(res, "not available") {
		t.Errorf("nil manager should report unavailable, got isErr=%v %q", isErr, res)
	}
}

func TestJobTools_UnknownID(t *testing.T) {
	m := newJobManager()
	m.setSeams(func(string) {}, func() {})
	tc := toolContext{Jobs: m}
	res, isErr := jobStatusTool(tc).handler(context.Background(), map[string]any{"job_id": "job_nope"})
	if !isErr {
		t.Errorf("unknown job_id should error: %q", res)
	}
}

// extractToken pulls a prefix+8hex token out of a reply.
func extractToken(t *testing.T, reply, prefix string) string {
	t.Helper()
	i := strings.Index(reply, prefix)
	if i < 0 {
		t.Fatalf("no %s token in reply: %q", prefix, reply)
	}
	end := i + len(prefix) + 8
	if end > len(reply) {
		end = len(reply)
	}
	return reply[i:end]
}

// waitFor polls cond until true or the deadline passes.
func waitFor(t *testing.T, cond func() bool, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %v", within)
	}
}
