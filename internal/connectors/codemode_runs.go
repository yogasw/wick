package connectors

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// CodeRunFilter narrows the Code Mode run audit. Empty fields match all.
type CodeRunFilter struct {
	UserID    string
	ProjectID string
	SessionID string
	Via       string
	Status    string
	Script    string
	From      *time.Time // inclusive lower bound on StartedAt
	To        *time.Time // inclusive upper bound on StartedAt
}

// CodeRunSummary is the header of the Code Mode audit page: how much ran,
// how it ended, and who and where it ran most.
type CodeRunSummary struct {
	Total         int64            `json:"total"`
	OK            int64            `json:"ok"`
	Failed        int64            `json:"failed"`
	ByStatus      map[string]int64 `json:"by_status"`
	AvgDurationMs float64          `json:"avg_duration_ms"`
	Calls         int64            `json:"calls"`
	CallErrors    int64            `json:"call_errors"`
	TopUsers      []CodeRunCount   `json:"top_users"`
	TopProjects   []CodeRunCount   `json:"top_projects"`
	TopScripts    []CodeRunCount   `json:"top_scripts"`
	TopErrors     []CodeRunCount   `json:"top_errors"`
}

// CodeRunCount is one row of a top-N list: a key and its run count, plus
// how many of those runs failed.
type CodeRunCount struct {
	Key    string `json:"key"`
	Runs   int64  `json:"runs"`
	Failed int64  `json:"failed"`
}

const codeRunTopN = 5

// RecordCodeRun stores one finished script run. The error text is capped
// like a connector run's.
func (r *Repo) RecordCodeRun(ctx context.Context, run *entity.CodeModeRun) error {
	if len(run.ErrorMsg) > maxRunErrorLen {
		run.ErrorMsg = run.ErrorMsg[:maxRunErrorLen]
	}
	return r.db.WithContext(ctx).Create(run).Error
}

// ListCodeRuns returns script runs matching f, newest first.
func (r *Repo) ListCodeRuns(ctx context.Context, f CodeRunFilter, limit, offset int) ([]entity.CodeModeRun, error) {
	var out []entity.CodeModeRun
	q := r.codeRunQuery(ctx, f).Order("started_at DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	return out, q.Find(&out).Error
}

// CountCodeRuns counts the runs ListCodeRuns pages through.
func (r *Repo) CountCodeRuns(ctx context.Context, f CodeRunFilter) (int64, error) {
	var n int64
	return n, r.codeRunQuery(ctx, f).Model(&entity.CodeModeRun{}).Count(&n).Error
}

// SummariseCodeRuns aggregates the runs matching f.
func (r *Repo) SummariseCodeRuns(ctx context.Context, f CodeRunFilter) (CodeRunSummary, error) {
	s := CodeRunSummary{ByStatus: map[string]int64{}}
	type statusRow struct {
		Status     string
		Count      int64
		AvgDur     float64
		Calls      int64
		CallErrors int64
	}
	var rows []statusRow
	err := r.codeRunQuery(ctx, f).Model(&entity.CodeModeRun{}).
		Select("status, COUNT(*) AS count, AVG(duration_ms) AS avg_dur, SUM(calls) AS calls, SUM(call_errors) AS call_errors").
		Group("status").Scan(&rows).Error
	if err != nil {
		return s, err
	}
	var durSum float64
	for _, row := range rows {
		s.Total += row.Count
		s.ByStatus[row.Status] = row.Count
		if entity.CodeModeRunStatus(row.Status) == entity.CodeModeRunOK {
			s.OK += row.Count
		} else {
			s.Failed += row.Count
		}
		s.Calls += row.Calls
		s.CallErrors += row.CallErrors
		durSum += row.AvgDur * float64(row.Count)
	}
	if s.Total > 0 {
		s.AvgDurationMs = durSum / float64(s.Total)
	}
	if s.TopUsers, err = r.topCodeRuns(ctx, f, "user_id", false); err != nil {
		return s, err
	}
	if s.TopProjects, err = r.topCodeRuns(ctx, f, "project_id", false); err != nil {
		return s, err
	}
	if s.TopScripts, err = r.topCodeRuns(ctx, f, "script", false); err != nil {
		return s, err
	}
	s.TopErrors, err = r.topCodeRuns(ctx, f, "error_msg", true)
	return s, err
}

// topCodeRuns groups the matching runs by col (a fixed column name, never
// user input) and returns the busiest keys. failedOnly keeps only runs
// that did not end ok, for the top-errors list.
func (r *Repo) topCodeRuns(ctx context.Context, f CodeRunFilter, col string, failedOnly bool) ([]CodeRunCount, error) {
	q := r.codeRunQuery(ctx, f).Model(&entity.CodeModeRun{}).Where(col + " <> ''")
	if failedOnly {
		q = q.Where("status <> ?", entity.CodeModeRunOK)
	}
	var out []CodeRunCount
	err := q.Select(col + " AS key, COUNT(*) AS runs, SUM(CASE WHEN status <> 'ok' THEN 1 ELSE 0 END) AS failed").
		Group(col).Order("runs DESC").Limit(codeRunTopN).Scan(&out).Error
	return out, err
}

// PurgeCodeRunsOlderThan deletes script runs started before cutoff; the
// connector-runs retention job calls it with the same cutoff.
func (r *Repo) PurgeCodeRunsOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("started_at < ?", cutoff).Delete(&entity.CodeModeRun{})
	return res.RowsAffected, res.Error
}

func (r *Repo) codeRunQuery(ctx context.Context, f CodeRunFilter) *gorm.DB {
	q := r.db.WithContext(ctx)
	if f.UserID != "" {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.ProjectID != "" {
		q = q.Where("project_id = ?", f.ProjectID)
	}
	if f.SessionID != "" {
		q = q.Where("session_id = ?", f.SessionID)
	}
	if f.Via != "" {
		q = q.Where("via = ?", f.Via)
	}
	if f.Status != "" {
		if f.Status == "failed" {
			q = q.Where("status <> ?", entity.CodeModeRunOK)
		} else {
			q = q.Where("status = ?", f.Status)
		}
	}
	if f.Script != "" {
		q = q.Where("script = ?", f.Script)
	}
	if f.From != nil {
		q = q.Where("started_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("started_at <= ?", *f.To)
	}
	return q
}
