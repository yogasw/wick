package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/job"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// cfgReader is the narrow view of internal/configs.Service the manager
// needs. Satisfied by *configs.Service; kept as an interface here so
// tests can supply fakes and manager stays import-free of internal/configs.
type cfgReader interface {
	GetOwned(owner, key string) string
}

// runHandle identifies one in-flight run owned by this process. The runID
// lets the run's own deferred cleanup tell "my entry" from a newer run's
// entry under the same key (cancel + immediate re-run reuses the key).
type runHandle struct {
	runID  string
	cancel context.CancelFunc
}

// Service manages job lifecycle: bootstrap from code-defined jobs,
// manual/scheduled execution, and result storage.
type Service struct {
	repo      *repo
	mu        sync.RWMutex
	runners   map[string]job.RunFunc  // key -> run func
	running   map[string]runHandle    // key -> in-flight run owned by this process
	cfg       cfgReader               // for injecting job.Ctx; may be nil in tests
}

func NewService(r *repo) *Service {
	return &Service{
		repo:    r,
		runners: make(map[string]job.RunFunc),
		running: make(map[string]runHandle),
	}
}

// NewServiceFromDB is a convenience constructor for callers that only
// have a *gorm.DB (e.g. the worker process and the web server).
func NewServiceFromDB(db *gorm.DB) *Service {
	return NewService(newRepo(db))
}

// SetConfigReader installs the configs-service view used to build a
// job.Ctx on every run. Called by wick at boot after both services
// have been constructed. Safe to skip in tests — Run() then sees a
// no-op Ctx where every Cfg(...) read returns "".
func (s *Service) SetConfigReader(c cfgReader) {
	s.cfg = c
}

// ResetStuckForJob exposes the per-job stuck-run sweep so the worker
// tick can call it inline while iterating its enabled-jobs list. See
// repo.ResetStuckForJob for the conditions a run must meet to be
// classified as stuck.
func (s *Service) ResetStuckForJob(ctx context.Context, j *entity.Job) (bool, error) {
	return s.repo.ResetStuckForJob(ctx, j)
}

// Bootstrap syncs code-defined jobs with the jobs table. New jobs get
// a row with their default cron; existing rows keep admin-managed fields.
// One module registration = one row.
func (s *Service) Bootstrap(ctx context.Context, mods []job.Module) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Reset any runs left in "running" state by a previous crash or
	// restart. Skips disabled jobs — they won't be re-triggered, so a
	// stuck row is cosmetic and not worth touching here. The worker
	// tick keeps sweeping every minute after this so post-startup
	// stalls also recover without intervention.
	allJobs, err := s.repo.ListJobs(ctx)
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("bootstrap: list jobs for stuck sweep failed")
	} else {
		count := 0
		for i := range allJobs {
			// Disabled jobs included: their stuck row is not cosmetic —
			// last_status="running" blocks Run Now (and the manual-run
			// guard) even while the job is disabled or once re-enabled.
			if allJobs[i].LastStatus != entity.JobStatusRunning {
				continue
			}
			reset, err := s.repo.ResetStuckForJob(ctx, &allJobs[i])
			if err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("job", allJobs[i].Key).Msg("bootstrap: reset stuck job failed")
				continue
			}
			if reset {
				count++
			}
		}
		if count > 0 {
			log.Ctx(ctx).Warn().Int("count", count).Msg("bootstrap: reset stuck running jobs from previous session")
		}
	}
	for _, mod := range mods {
		m := mod.Meta
		if _, dup := s.runners[m.Key]; dup {
			return fmt.Errorf("bootstrap job: duplicate key %q", m.Key)
		}
		s.runners[m.Key] = mod.Run
		row := &entity.Job{
			Key:         m.Key,
			Name:        m.Name,
			Description: m.Description,
			Icon:        m.Icon,
			Schedule:    m.DefaultCron,
		}
		if err := s.repo.UpsertJob(ctx, row); err != nil {
			return fmt.Errorf("bootstrap job %s: %w", m.Key, err)
		}
		if m.AutoEnable {
			if err := s.repo.ForceEnable(ctx, m.Key); err != nil {
				return fmt.Errorf("bootstrap job %s: force enable: %w", m.Key, err)
			}
		}
	}
	return nil
}

func (s *Service) ListJobs(ctx context.Context) ([]entity.Job, error) {
	return s.repo.ListJobs(ctx)
}

func (s *Service) ListEnabledJobs(ctx context.Context) ([]entity.Job, error) {
	return s.repo.ListEnabledJobs(ctx)
}

func (s *Service) GetJob(ctx context.Context, key string) (*entity.Job, error) {
	return s.repo.GetJobByKey(ctx, key)
}

func (s *Service) UpdateSchedule(ctx context.Context, key string, schedule string, enabled bool, maxRuns int, maxTimeoutMin int) error {
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateSchedule(ctx, j.ID, schedule, enabled, maxRuns, maxTimeoutMin); err != nil {
		return err
	}
	// Disabling stops a run in flight. A disabled job that keeps
	// executing surprises the admin, and its last_status="running"
	// leftover would block the first manual run after re-enable.
	if !enabled {
		stopped := s.stopLocal(j.Key)
		if stopped || j.LastStatus == entity.JobStatusRunning {
			if err := s.repo.CancelOpenRuns(ctx, j.ID, "cancelled: job disabled"); err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("job", j.Key).Msg("disable: cancel open runs failed")
			}
			if err := s.repo.SetStatus(ctx, j.ID, entity.JobStatusIdle); err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("job", j.Key).Msg("disable: set idle failed")
			}
		}
	}
	return nil
}

// stillRunning reports whether j is genuinely executing right now: this
// process owns a live run, or the DB shows an open run younger than the
// job's timeout (another process owns it). When neither holds — the
// last_status="running" row is stale — it repairs the row and returns
// false, so a stuck job never needs manual DB surgery to run again.
func (s *Service) stillRunning(ctx context.Context, j *entity.Job) bool {
	s.mu.RLock()
	_, inProcess := s.running[j.Key]
	s.mu.RUnlock()
	if inProcess {
		return true
	}
	timeout := j.MaxTimeoutMin
	if timeout <= 0 {
		timeout = 30
	}
	cutoff := time.Now().Add(-time.Duration(timeout) * time.Minute)
	fresh, err := s.repo.FreshOpenRunExists(ctx, j.ID, cutoff)
	if err != nil || fresh {
		// On a read error assume running: refusing a trigger is cheaper
		// than a double run.
		return true
	}
	if _, err := s.repo.ResetStuckForJob(ctx, j); err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("job", j.Key).Msg("reconcile stale running status failed")
		return true
	}
	return false
}

// RunManual triggers a job run initiated by a user. Returns the run ID.
func (s *Service) RunManual(ctx context.Context, key string, userID string) (string, error) {
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return "", fmt.Errorf("job not found: %w", err)
	}
	if j.MaxRuns > 0 && j.TotalRuns >= j.MaxRuns {
		return "", fmt.Errorf("job has reached the maximum number of runs (%d)", j.MaxRuns)
	}
	if j.LastStatus == entity.JobStatusRunning && s.stillRunning(ctx, j) {
		return "", fmt.Errorf("job is already running")
	}
	return s.execute(ctx, j, entity.RunTriggerManual, userID)
}

// RunCron triggers a job run initiated by the scheduler.
func (s *Service) RunCron(ctx context.Context, key string) (string, error) {
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return "", fmt.Errorf("job not found: %w", err)
	}
	if j.MaxRuns > 0 && j.TotalRuns >= j.MaxRuns {
		return "", fmt.Errorf("max runs reached")
	}
	if j.LastStatus == entity.JobStatusRunning && s.stillRunning(ctx, j) {
		return "", fmt.Errorf("job is already running")
	}
	return s.execute(ctx, j, entity.RunTriggerCron, "")
}

func (s *Service) execute(ctx context.Context, j *entity.Job, trigger entity.RunTrigger, userID string) (string, error) {
	s.mu.RLock()
	runFn, ok := s.runners[j.Key]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no job implementation found for key %q", j.Key)
	}

	now := time.Now()
	run := &entity.JobRun{
		JobID:       j.ID,
		Status:      entity.RunStatusRunning,
		TriggeredBy: trigger,
		UserID:      userID,
		StartedAt:   now,
	}
	if err := s.repo.CreateRun(ctx, run); err != nil {
		return "", fmt.Errorf("create run: %w", err)
	}

	_ = s.repo.SetStatus(ctx, j.ID, entity.JobStatusRunning)

	timeoutMin := j.MaxTimeoutMin
	if timeoutMin <= 0 {
		timeoutMin = 30
	}
	bgCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMin)*time.Minute)
	s.mu.Lock()
	s.running[j.Key] = runHandle{runID: run.ID, cancel: cancel}
	s.mu.Unlock()

	go func() {
		l := log.With().Str("job", j.Key).Str("run_id", run.ID).Logger()

		// finalize closes out the run + job rows. Uses a fresh
		// context detached from bgCtx because bgCtx may already be
		// timed-out or canceled by the time we get here — using it
		// would silently fail the UPDATE queries and leave the rows
		// stuck in "running" forever. The 10 s budget is generous
		// for a couple of indexed UPDATEs but bounded so a slow DB
		// doesn't hang the goroutine on shutdown.
		finalize := func(status entity.RunStatus, result string) {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			if err := s.repo.FinishRun(cleanupCtx, run.ID, status, result); err != nil {
				l.Error().Err(err).Msg("failed to finish run")
			}
			_ = s.repo.IncrementRuns(cleanupCtx, j.ID)
			// Conditional idle: after cancel + immediate re-run, this
			// (late) finalize must not clobber the newer run's badge.
			_ = s.repo.SetIdleIfNoOpenRun(cleanupCtx, j.ID)
		}

		defer func() {
			// Delete only our own entry: after cancel + immediate re-run
			// the key already holds the newer run's handle, and blindly
			// deleting it would make that live run invisible to Cancel.
			s.mu.Lock()
			if h, ok := s.running[j.Key]; ok && h.runID == run.ID {
				delete(s.running, j.Key)
			}
			s.mu.Unlock()
			cancel()
			// Catch panics in the RunFunc so a misbehaving job
			// doesn't leave its row stuck in "running" forever.
			// Without this, recovery depended on the tick sweep
			// (≤ max_timeout_min) or a server restart.
			if rec := recover(); rec != nil {
				l.Error().Interface("panic", rec).Msg("job run panicked")
				finalize(entity.RunStatusError, fmt.Sprintf("panic: %v", rec))
			}
		}()

		runCtx := bgCtx
		if s.cfg != nil {
			runCtx = job.WithCtx(bgCtx, job.NewCtx(j.Key, s.cfg))
		}

		result, runErr := runFn(runCtx)

		status := entity.RunStatusSuccess
		switch {
		case runErr == nil:
			l.Info().Msg("job run completed")
		case errors.Is(runErr, context.Canceled) && bgCtx.Err() == context.Canceled:
			// The run ended because ITS OWN context was canceled — the
			// Cancel button, a disable, or shutdown stopped it on purpose.
			// That is a cancellation, not a failure: without this, whenever
			// this finalize wins the race against the cancel path's
			// CancelOpenRuns (which only touches still-open rows), the row
			// ended up "error" for a run the user deliberately stopped.
			// Timeouts don't take this branch (bgCtx reports
			// DeadlineExceeded) — they stay errors.
			status = entity.RunStatusCancelled
			if result == "" {
				result = "cancelled"
			}
			l.Info().Msg("job run cancelled")
		default:
			status = entity.RunStatusError
			if result == "" {
				result = runErr.Error()
			}
			l.Error().Err(runErr).Msg("job run failed")
		}
		finalize(status, result)
	}()

	return run.ID, nil
}

// stopLocal cancels the in-process run for key, if this process owns one.
// Reports whether there was one. The map entry is removed here so a
// follow-up Run Now can start immediately; the run's own deferred cleanup
// is runID-guarded and won't touch a newer entry.
func (s *Service) stopLocal(key string) bool {
	s.mu.Lock()
	h, ok := s.running[key]
	if ok {
		delete(s.running, key)
	}
	s.mu.Unlock()
	if ok {
		h.cancel()
	}
	return ok
}

// CancelJob stops a running job by key. It cancels the in-process run
// when this process owns one, then closes the books either way: open run
// rows become "cancelled" and last_status flips to idle. Called on a job
// whose row is stale (DB says running, nothing actually is) it repairs
// the row instead of failing, so Cancel is also the operator's unstick
// button. Errors only when the job is idle everywhere.
//
// The context cancel is best-effort by nature: a RunFunc that ignores its
// context keeps executing until its timeout, but the DB no longer calls
// the job running, its row can't block future triggers, and any late
// finalize is guarded (FinishRun / SetIdleIfNoOpenRun) so it can't
// resurrect the cancelled state.
func (s *Service) CancelJob(ctx context.Context, key string) error {
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return err
	}
	stopped := s.stopLocal(j.Key)
	hasOpen, err := s.repo.HasOpenRun(ctx, j.ID)
	if err != nil {
		return err
	}
	if !stopped && !hasOpen && j.LastStatus != entity.JobStatusRunning {
		return fmt.Errorf("job %q is not running", key)
	}
	if err := s.repo.CancelOpenRuns(ctx, j.ID, "cancelled by user"); err != nil {
		return err
	}
	return s.repo.SetStatus(ctx, j.ID, entity.JobStatusIdle)
}

func (s *Service) GetRun(ctx context.Context, runID string) (*entity.JobRun, error) {
	return s.repo.GetRun(ctx, runID)
}

func (s *Service) ListRuns(ctx context.Context, key string, limit int) ([]entity.JobRun, error) {
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	return s.repo.ListRuns(ctx, j.ID, limit)
}
