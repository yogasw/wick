package wick

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// job.go is P3 of the long-running-tool plan: a generic async job model
// with completion notification. It sits ABOVE the background shell (P2):
// where a background shell must be polled by hand, a job runs any runner
// off-turn and, when it finishes, INJECTS a turn back into the session so
// the agent is told "[job-done …]" without polling. That inject is the
// whole point — it's what lets the model kick off a 20-minute install,
// answer the user, and be re-woken when it's done.
//
// A job manager is per-spawn (like bgRegistry). Two seams are wired by
// the spawner:
//   - inject(text): push a synthetic user turn into the engine's msgs
//     channel (process.go). Runs the [job-done] turn.
//   - keepAlive: an emit of a heartbeat frame so the pool's idle-kill
//     timer doesn't reap the session while a job runs off-turn with no
//     other stdout activity (a 20m job would otherwise die at the 120s
//     idle timeout, and the inject would land on a dead channel).

// JobStatus is the lifecycle state of a job.
type JobStatus string

const (
	jobRunning   JobStatus = "running"
	jobSucceeded JobStatus = "succeeded"
	jobFailed    JobStatus = "failed"
	jobCancelled JobStatus = "cancelled"
)

// jobRunner executes the actual work. It writes output to out as it goes
// and returns an exit code (0 = success) plus an error for a runner-level
// failure (spawn error, etc.). ctx cancellation must stop the work
// promptly — the manager cancels it on job_cancel and on teardown.
type jobRunner func(ctx context.Context, out io.Writer) (exitCode int, err error)

// jobEntry is one job's record + control.
type jobEntry struct {
	id     string
	tool   string // runner label, e.g. "shell"
	label  string // human summary, e.g. the command
	cancel context.CancelFunc

	mu       sync.Mutex
	buf      capBuffer
	status   JobStatus
	exitCode int
	errMsg   string
	started  time.Time
	ended    time.Time
}

func (j *jobEntry) snapshot() (status JobStatus, out string, truncated bool, exitCode int, errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status, j.buf.String(), j.buf.truncated, j.exitCode, j.errMsg
}

// jobWriter funnels runner output into the entry's capped buffer under
// its lock (status readers race it).
type jobWriter struct{ j *jobEntry }

func (w jobWriter) Write(p []byte) (int, error) {
	w.j.mu.Lock()
	defer w.j.mu.Unlock()
	return w.j.buf.Write(p)
}

// maxJobs caps concurrent jobs per session — same rationale as maxBgProcs.
const maxJobs = 16

// jobManager owns the jobs for one spawn and the two engine seams.
type jobManager struct {
	mu   sync.Mutex
	jobs map[string]*jobEntry

	// inject pushes a synthetic user turn into the engine. Set by the
	// spawner; nil disables completion injection (the job still runs and
	// is pollable, it just won't auto-wake the session).
	inject func(text string)
	// keepAlive emits a heartbeat frame so the idle-kill timer doesn't
	// reap the session while a job runs off-turn. nil = no heartbeat.
	keepAlive func()

	// active counts running jobs; the heartbeat loop runs only while >0.
	active   int
	hbCancel context.CancelFunc
}

func newJobManager() *jobManager {
	return &jobManager{jobs: map[string]*jobEntry{}}
}

// setSeams wires the engine callbacks. Called by the spawner once the
// process + engine exist.
func (m *jobManager) setSeams(inject func(string), keepAlive func()) {
	m.mu.Lock()
	m.inject = inject
	m.keepAlive = keepAlive
	m.mu.Unlock()
}

// start launches a runner as a job and returns its id. The job runs on
// its own goroutine; when it ends, the manager records the outcome and
// injects a [job-done] turn (if the inject seam is wired).
func (m *jobManager) start(parent context.Context, tool, label string, run jobRunner) (string, error) {
	m.mu.Lock()
	if len(m.jobs) >= maxJobs {
		m.mu.Unlock()
		return "", fmt.Errorf("too many jobs (max %d); cancel some with job_cancel first", maxJobs)
	}
	id := "job_" + uuid.NewString()[:8]
	ctx, cancel := context.WithCancel(parent)
	je := &jobEntry{id: id, tool: tool, label: label, cancel: cancel, status: jobRunning, started: time.Now()}
	m.jobs[id] = je
	m.active++
	m.ensureHeartbeatLocked(parent)
	m.mu.Unlock()

	go m.run(ctx, je, run)
	log.Debug().Str("job_id", id).Str("tool", tool).Str("label", label).Msg("wick.job: started")
	return id, nil
}

// run executes the runner, records the terminal state, decrements the
// active count (stopping the heartbeat when it hits 0), and injects the
// completion turn.
func (m *jobManager) run(ctx context.Context, je *jobEntry, runner jobRunner) {
	exitCode, err := runner(ctx, jobWriter{j: je})

	je.mu.Lock()
	je.ended = time.Now()
	switch {
	case je.status == jobCancelled:
		// job_cancel already set the terminal state; keep it.
	case ctx.Err() == context.Canceled:
		je.status = jobCancelled
	case err != nil:
		je.status = jobFailed
		je.errMsg = err.Error()
		je.exitCode = exitCode
	case exitCode != 0:
		je.status = jobFailed
		je.exitCode = exitCode
	default:
		je.status = jobSucceeded
		je.exitCode = 0
	}
	status := je.status
	exit := je.exitCode
	je.mu.Unlock()

	m.mu.Lock()
	m.active--
	if m.active <= 0 {
		m.stopHeartbeatLocked()
	}
	inject := m.inject
	m.mu.Unlock()

	log.Debug().Str("job_id", je.id).Str("status", string(status)).Int("exit", exit).Msg("wick.job: finished")

	if inject != nil {
		inject(jobDoneMessage(je.id, je.tool, je.label, status, exit))
	}
}

// jobDoneMessage is the synthetic user turn injected on completion. It's
// terse and machine-parseable so the model can react (report to the user,
// read the log, retry) without re-polling. Kept as a [job-done …] marker
// line the model is told about in the system prompt / tool descriptions.
func jobDoneMessage(id, tool, label string, status JobStatus, exit int) string {
	ok := status == jobSucceeded
	return fmt.Sprintf(
		"[job-done id=%s tool=%s ok=%t status=%s exit=%d] %s — read its full output with job_log(job_id=%q) if you need details, then continue.",
		id, tool, ok, status, exit, label, id)
}

// status returns a job snapshot for job_status.
func (m *jobManager) get(id string) (*jobEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	je, ok := m.jobs[id]
	return je, ok
}

// cancel stops a running job. Idempotent.
func (m *jobManager) cancel(id string) error {
	m.mu.Lock()
	je, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no job with job_id %q", id)
	}
	je.mu.Lock()
	if je.status == jobRunning {
		je.status = jobCancelled
	}
	je.mu.Unlock()
	je.cancel()
	return nil
}

// cancelAll stops every running job. Called on spawn teardown so a job's
// runner (and its process tree, for the shell runner) doesn't outlive the
// session.
func (m *jobManager) cancelAll() {
	m.mu.Lock()
	entries := make([]*jobEntry, 0, len(m.jobs))
	for _, je := range m.jobs {
		entries = append(entries, je)
	}
	m.stopHeartbeatLocked()
	m.mu.Unlock()
	for _, je := range entries {
		je.cancel()
	}
}

// ensureHeartbeatLocked starts the keepalive loop if not already running.
// Caller holds m.mu. The loop emits a heartbeat frame periodically so the
// pool idle-kill timer (which only resets on stdout activity) doesn't reap
// the session during an off-turn job.
func (m *jobManager) ensureHeartbeatLocked(parent context.Context) {
	if m.hbCancel != nil || m.keepAlive == nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.hbCancel = cancel
	beat := m.keepAlive
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				beat()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// stopHeartbeatLocked stops the keepalive loop. Caller holds m.mu.
func (m *jobManager) stopHeartbeatLocked() {
	if m.hbCancel != nil {
		m.hbCancel()
		m.hbCancel = nil
	}
}
