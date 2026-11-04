package connectors

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// AuditCount is one row of an audit breakdown: a key, its run count and
// how many of those runs ended in error. Op is set only on the
// per-operation breakdown, whose Key is then the connector instance id.
type AuditCount struct {
	Key    string `json:"key"`
	Op     string `json:"op,omitempty"`
	Runs   int64  `json:"runs"`
	Failed int64  `json:"failed"`
}

// AuditBreakdown is where the runs matching a filter went: per instance,
// per operation, per user, per session, per source, and the most common
// errors.
type AuditBreakdown struct {
	Instances  []AuditCount `json:"instances"`
	Operations []AuditCount `json:"operations"`
	Users      []AuditCount `json:"users"`
	Sessions   []AuditCount `json:"sessions"`
	Sources    []AuditCount `json:"sources"`
	Errors     []AuditCount `json:"errors"`
}

const auditTopN = 8

// AuditBreakdown groups the runs matching f for the audit page's summary.
func (r *Repo) AuditBreakdown(ctx context.Context, f AuditFilter) (AuditBreakdown, error) {
	var b AuditBreakdown
	var err error
	if b.Instances, err = r.auditTop(ctx, f, "connector_id", "", auditTopN); err != nil {
		return b, err
	}
	if b.Operations, err = r.auditTop(ctx, f, "connector_id", "operation_key", auditTopN); err != nil {
		return b, err
	}
	if b.Users, err = r.auditTop(ctx, f, "user_id", "", auditTopN); err != nil {
		return b, err
	}
	if b.Sessions, err = r.auditTop(ctx, f, "session_id", "", auditTopN); err != nil {
		return b, err
	}
	if b.Sources, err = r.auditTop(ctx, f, "source", "", 0); err != nil {
		return b, err
	}
	ef := f
	if ef.Status == "" {
		ef.Status = string(entity.ConnectorRunStatusError)
	}
	b.Errors, err = r.auditTop(ctx, ef, "error_msg", "", auditTopN)
	return b, err
}

// AuditOptions lists the instances and operations that have runs matching
// f, busiest first, to fill the page's connector → instance → operation
// pickers. The caller clears the picker fields it wants options for.
func (r *Repo) AuditOptions(ctx context.Context, f AuditFilter, limit int) ([]AuditCount, error) {
	return r.auditTop(ctx, f, "connector_id", "operation_key", limit)
}

// auditTop groups by col (and col2 when set) — fixed column names, never
// user input. limit 0 = all groups.
func (r *Repo) auditTop(ctx context.Context, f AuditFilter, col, col2 string, limit int) ([]AuditCount, error) {
	sel := col + " AS key"
	group := col
	if col2 != "" {
		sel += ", " + col2 + " AS op"
		group += ", " + col2
	}
	sel += ", COUNT(*) AS runs, SUM(CASE WHEN status = 'error' THEN 1 ELSE 0 END) AS failed"
	q := r.auditFilterQuery(ctx, f).Model(&entity.ConnectorRun{}).Where(col + " <> ''").
		Select(sel).Group(group).Order("runs DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var out []AuditCount
	return out, q.Scan(&out).Error
}

// whereAnyOf filters col on one value or a comma-separated list.
func whereAnyOf(q *gorm.DB, col, v string) *gorm.DB {
	vals := splitList(v)
	switch len(vals) {
	case 0:
		return q
	case 1:
		return q.Where(col+" = ?", vals[0])
	default:
		return q.Where(col+" IN ?", vals)
	}
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
