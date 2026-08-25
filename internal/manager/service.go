package manager

import (
	"context"
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

// Service manages job lifecycle: bootstrap from code-defined jobs,
// manual/scheduled execution, and result storage.
type Service struct {
	repo      *repo
	mu        sync.RWMutex
	runners   map[string]job.RunFunc    // key -> run func
	cancels   map[string]context.CancelFunc // key -> cancel for running job
	cfg       cfgReader                 // for injecting job.Ctx; may be nil in tests
}

func NewService(r *repo) *Service {
	return &Service{
		repo:    r,
		runners: make(map[string]job.RunFunc),
		cancels: make(map[string]context.CancelFunc),
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
	enabledJobs, err := s.repo.ListEnabledJobs(ctx)
	if err != nil {
		log.Ctx(ctx).Warn().Err(err).Msg("bootstrap: list enabled for stuck sweep failed")
	} else {
		count := 0
		for i := range enabledJobs {
			reset, err := s.repo.ResetStuckForJob(ctx, &enabledJobs[i])
			if err != nil {
				log.Ctx(ctx).Warn().Err(err).Str("job", enabledJobs[i].Key).Msg("bootstrap: reset stuck job failed")
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
	return s.repo.UpdateSchedule(ctx, j.ID, schedule, enabled, maxRuns, maxTimeoutMin)
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
	if j.LastStatus == entity.JobStatusRunning {
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
	if j.LastStatus == entity.JobStatusRunning {
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
	s.cancels[j.Key] = cancel
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
			_ = s.repo.SetStatus(cleanupCtx, j.ID, entity.JobStatusIdle)
		}

		defer func() {
			s.mu.Lock()
			delete(s.cancels, j.Key)
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
		if runErr != nil {
			status = entity.RunStatusError
			if result == "" {
				result = runErr.Error()
			}
			l.Error().Err(runErr).Msg("job run failed")
		} else {
			l.Info().Msg("job run completed")
		}
		finalize(status, result)
	}()

	return run.ID, nil
}

// CancelJob cancels a running job by key. Returns error if job not running.
func (s *Service) CancelJob(ctx context.Context, key string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[key]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %q is not running", key)
	}
	cancel()
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return err
	}
	_ = s.repo.SetStatus(ctx, j.ID, entity.JobStatusIdle)
	return nil
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
