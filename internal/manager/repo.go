package manager

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/entity"
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
	err := r.db.WithContext(ctx).Where("`key` = ?", key).First(&j).Error
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

func (r *repo) UpdateSchedule(ctx context.Context, id string, schedule string, enabled bool, maxRuns int) error {
	return r.db.WithContext(ctx).Model(&entity.Job{}).Where("id = ?", id).
		Updates(map[string]any{
			"schedule":   schedule,
			"enabled":    enabled,
			"max_runs":   maxRuns,
			"updated_at": time.Now(),
		}).Error
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
			"result":   result,
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
