package manager

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/strutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type repo struct {
	db *gorm.DB
}

func newRepo(db *gorm.DB) *repo {
	return &repo{db: db}
}

func (r *repo) UpsertJob(ctx context.Context, j *entity.Job) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"name", "description", "icon", "updated_at"}),
	}).Create(j).Error
}

func (r *repo) GetJobByKey(ctx context.Context, key string) (*entity.Job, error) {
	var j entity.Job
	err := r.db.WithContext(ctx).Where(`"key" = ?`, key).First(&j).Error
	return &j, err
}

func (r *repo) GetJobByID(ctx context.Context, id string) (*entity.Job, error) {
	var j entity.Job
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&j).Error
	return &j, err
}

func (r *repo) ListJobs(ctx context.Context) ([]entity.Job, error) {
	var js []entity.Job
	err := r.db.WithContext(ctx).Order("name").Find(&js).Error
	return js, err
}

func (r *repo) ListEnabledJobs(ctx context.Context) ([]entity.Job, error) {
	var js []entity.Job
	err := r.db.WithContext(ctx).Where("enabled = ?", true).Order("name").Find(&js).Error
	return js, err
}

// ForceEnable sets Enabled=true on the job with the given key. Used by
// Bootstrap when Meta.AutoEnable is set, so built-in maintenance jobs
// stay enabled across restarts even if something flipped the flag (or
// the row was never enabled by an admin to begin with).
func (r *repo) ForceEnable(ctx context.Context, key string) error {
	return r.db.WithContext(ctx).Model(&entity.Job{}).Where(`"key" = ?`, key).
		Updates(map[string]any{
			"enabled":    true,
			"updated_at": time.Now(),
		}).Error
}

func (r *repo) UpdateSchedule(ctx context.Context, id string, schedule string, enabled bool, maxRuns int, maxTimeoutMin int) error {
	return r.db.WithContext(ctx).Model(&entity.Job{}).Where("id = ?", id).
		Updates(map[string]any{
			"schedule":         schedule,
			"enabled":          enabled,
			"max_runs":         maxRuns,
			"max_timeout_min":  maxTimeoutMin,
			"updated_at":       time.Now(),
		}).Error
}

// ResetStuckForJob marks any open run of job j whose started_at is
// older than max_timeout_min as error, then flips j's last_status to
// idle when no open run remains. Returns true when the row was reset.
//
// Per-job (not a bulk sweep) for two reasons:
//
//   - The worker tick already iterates the enabled-jobs list to
//     evaluate cron triggers. Sweeping per-job inside that same loop
//     piggybacks on data we already have, no extra "list enabled"
//     query.
//   - Disabled / archived jobs are skipped automatically: the caller
//     simply doesn't pass them in. As job_runs history grows, scan
//     size stays bounded by the (small) active set.
//
// The previous bulk version had a Postgres-only `interval '1 minute'`
// math in SQL that errored silently on SQLite and let stuck rows pile
// up across restarts. Doing the cutoff math in Go keeps this portable
// across both drivers with no dialect branching.
func (r *repo) ResetStuckForJob(ctx context.Context, j *entity.Job) (bool, error) {
	if j == nil {
		return false, nil
	}
	timeout := j.MaxTimeoutMin
	if timeout <= 0 {
		timeout = 30
	}
	cutoff := time.Now().Add(-time.Duration(timeout) * time.Minute)

	var stuckIDs []string
	if err := r.db.WithContext(ctx).Model(&entity.JobRun{}).
		Where("job_id = ? AND ended_at IS NULL AND started_at < ?", j.ID, cutoff).
		Pluck("id", &stuckIDs).Error; err != nil {
		return false, err
	}
	if len(stuckIDs) == 0 {
		return false, nil
	}

	now := time.Now()
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return false, tx.Error
	}
	defer func() {
		if rec := recover(); rec != nil {
			tx.Rollback()
		}
	}()

	if err := tx.Model(&entity.JobRun{}).
		Where("id IN ?", stuckIDs).
		Updates(map[string]any{
			"status":   entity.RunStatusError,
			"result":   "timed out (max_timeout exceeded)",
			"ended_at": &now,
		}).Error; err != nil {
		tx.Rollback()
		return false, err
	}

	// Flip job.last_status to idle only when no other open run remains.
	// A manual trigger fired right after a stuck cron tick should keep
	// running — that fresh run's row still has ended_at IS NULL.
	res := tx.Model(&entity.Job{}).
		Where("id = ? AND last_status = ?", j.ID, entity.JobStatusRunning).
		Where("NOT EXISTS (?)",
			tx.Model(&entity.JobRun{}).Select("1").
				Where("job_runs.job_id = ? AND job_runs.ended_at IS NULL", j.ID)).
		Updates(map[string]any{
			"last_status": entity.JobStatusIdle,
			"updated_at":  now,
		})
	if res.Error != nil {
		tx.Rollback()
		return false, res.Error
	}

	return res.RowsAffected > 0, tx.Commit().Error
}

func (r *repo) SetStatus(ctx context.Context, id string, status entity.JobStatus) error {
	updates := map[string]any{
		"last_status": status,
		"updated_at":  time.Now(),
	}
	if status != entity.JobStatusRunning {
		now := time.Now()
		updates["last_run_at"] = &now
	}
	return r.db.WithContext(ctx).Model(&entity.Job{}).Where("id = ?", id).Updates(updates).Error
}

func (r *repo) IncrementRuns(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&entity.Job{}).Where("id = ?", id).
		UpdateColumn("total_runs", gorm.Expr("total_runs + 1")).Error
}

func (r *repo) CreateRun(ctx context.Context, run *entity.JobRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

func (r *repo) FinishRun(ctx context.Context, runID string, status entity.RunStatus, result string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&entity.JobRun{}).Where("id = ?", runID).
		Updates(map[string]any{
			"status":   status,
			"result":   strutil.LimitText(result, strutil.DefaultLimit),
			"ended_at": &now,
		}).Error
}

func (r *repo) GetRun(ctx context.Context, runID string) (*entity.JobRun, error) {
	var run entity.JobRun
	err := r.db.WithContext(ctx).Where("id = ?", runID).First(&run).Error
	return &run, err
}

func (r *repo) ListRuns(ctx context.Context, jobID string, limit int) ([]entity.JobRun, error) {
	var runs []entity.JobRun
	q := r.db.WithContext(ctx).Where("job_id = ?", jobID).Order("started_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Find(&runs).Error
	return runs, err
}
