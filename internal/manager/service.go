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
	repo    *repo
	mu      sync.RWMutex
	runners map[string]job.RunFunc // key -> run func
	cfg     cfgReader              // for injecting job.Ctx; may be nil in tests
}

func NewService(r *repo) *Service {
	return &Service{
		repo:    r,
		runners: make(map[string]job.RunFunc),
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

// Bootstrap syncs code-defined jobs with the jobs table. New jobs get
// a row with their default cron; existing rows keep admin-managed fields.
// One module registration = one row.
func (s *Service) Bootstrap(ctx context.Context, mods []job.Module) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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

func (s *Service) UpdateSchedule(ctx context.Context, key string, schedule string, enabled bool, maxRuns int) error {
	j, err := s.repo.GetJobByKey(ctx, key)
	if err != nil {
		return err
	}
	return s.repo.UpdateSchedule(ctx, j.ID, schedule, enabled, maxRuns)
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

	go func() {
		bgCtx := context.Background()
		if s.cfg != nil {
			bgCtx = job.WithCtx(bgCtx, job.NewCtx(j.Key, s.cfg))
		}
		l := log.With().Str("job", j.Key).Str("run_id", run.ID).Logger()

		result, runErr := runFn(bgCtx)

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

		if err := s.repo.FinishRun(bgCtx, run.ID, status, result); err != nil {
			l.Error().Err(err).Msg("failed to finish run")
		}
		_ = s.repo.IncrementRuns(bgCtx, j.ID)
		_ = s.repo.SetStatus(bgCtx, j.ID, entity.JobStatusIdle)
	}()

	return run.ID, nil
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
