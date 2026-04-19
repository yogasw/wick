package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// JobStatus represents the current state of a job.
type JobStatus string

const (
	JobStatusIdle    JobStatus = "idle"
	JobStatusRunning JobStatus = "running"
)

// RunStatus represents the outcome of a single job execution.
type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusError   RunStatus = "error"
)

// RunTrigger describes how a run was initiated.
type RunTrigger string

const (
	RunTriggerManual RunTrigger = "manual"
	RunTriggerCron   RunTrigger = "cron"
)

// Job is a background job definition whose schedule and lifecycle are
// managed via the DB. Code-defined jobs bootstrap a row on startup; admins
// can tweak the cron expression, enable/disable, and cap the run count.
type Job struct {
	ID          string    `gorm:"type:varchar(36);primaryKey"`
	Key         string    `gorm:"type:varchar(100);uniqueIndex;not null"`
	Name        string    `gorm:"type:varchar(255);not null"`
	Description string    `gorm:"type:text"`
	Icon        string    `gorm:"type:varchar(10)"`
	Schedule    string    `gorm:"type:varchar(100)"` // cron expression
	Enabled     bool      `gorm:"default:false"`
	MaxRuns     int       `gorm:"default:0"` // 0 = unlimited, admin-managed
	TotalRuns   int       `gorm:"default:0"`
	LastStatus  JobStatus `gorm:"type:varchar(20);default:'idle'"`
	LastRunAt   *time.Time
	CreatedBy   string `gorm:"type:varchar(36)"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (j *Job) BeforeCreate(tx *gorm.DB) error {
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	return nil
}

// JobRun stores the result of a single execution of a Job.
type JobRun struct {
	ID          string     `gorm:"type:varchar(36);primaryKey"`
	JobID       string     `gorm:"type:varchar(36);index;not null"`
	Status      RunStatus  `gorm:"type:varchar(20);not null"`
	Result      string     `gorm:"type:text"`
	TriggeredBy RunTrigger `gorm:"type:varchar(20);not null"`
	UserID      string     `gorm:"type:varchar(36)"`
	StartedAt   time.Time
	EndedAt     *time.Time
	CreatedAt   time.Time
}

func (r *JobRun) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}
