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

// ResetStuckRuns resets only runs that have exceeded their job's max_timeout_min.
// A run is considered stuck when ended_at IS NULL and
// started_at < now - COALESCE(max_timeout_min, 30) minutes.
// This preserves legitimately long-running jobs whose timeout has not elapsed.
// Returns the number of jobs whose status was reset to idle.
func (r *repo) ResetStuckRuns(ctx context.Context) (int, error) {
	now := time.Now()
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Step 1: mark timed-out open runs as error.
	if err := tx.Exec(`
		UPDATE job_runs
		SET status = ?, result = ?, ended_at = ?
		WHERE ended_at IS NULL
		  AND EXISTS (
		    SELECT 1 FROM jobs
		    WHERE jobs.id = job_runs.job_id
		      AND job_runs.started_at < ? - (COALESCE(NULLIF(jobs.max_timeout_min, 0), 30) * interval '1 minute')
		  )
	`, entity.RunStatusError, "timed out (server restart)", now, now).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	// Step 2: reset job status to idle where no open run remains.
	res := tx.Exec(`
		UPDATE jobs
		SET last_status = ?, updated_at = ?
		WHERE last_status = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM job_runs
		    WHERE job_runs.job_id = jobs.id
		      AND job_runs.ended_at IS NULL
		  )
	`, entity.JobStatusIdle, now, entity.JobStatusRunning)
	if res.Error != nil {
		tx.Rollback()
		return 0, res.Error
	}

	return int(res.RowsAffected), tx.Commit().Error
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
