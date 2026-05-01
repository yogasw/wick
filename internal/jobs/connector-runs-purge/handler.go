// Package connectorrunspurge is wick's built-in maintenance job that
// trims old rows from the connector_runs audit table. It is registered
// by internal/pkg/worker.NewServer (not internal/connectors.RegisterBuiltins)
// because the RunFunc needs a *gorm.DB handle, which is only available
// after the worker has initialized the database.
//
// Default cron: 03:00 daily. Default retention: 7 days. Both editable
// per-instance from the manager UI.
//
// Backed by connectors.Repo.PurgeRunsOlderThan, which uses the
// standalone started_at index on connector_runs — a single range delete,
// cheap even when the table is large.
package connectorrunspurge

import (
	"context"
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/job"

	"gorm.io/gorm"
)

// defaultRetentionDays is the in-code fallback used when the Config
// row is missing or carries a non-positive value. Matches the design
// note in internal/docs/connectors-design.md sec. 5.3.
const defaultRetentionDays = 7

// NewRun returns a job.RunFunc bound to the given DB handle. The
// handle is captured in the closure so the registry doesn't need to
// know about wick internals at registration time — see godoc on
// pkg/job.RunFunc for the factory pattern.
func NewRun(db *gorm.DB) job.RunFunc {
	repo := connectors.NewRepo(db)
	return func(ctx context.Context) (string, error) {
		days := job.FromContext(ctx).CfgInt("retention_days")
		if days <= 0 {
			days = defaultRetentionDays
		}
		cutoff := time.Now().AddDate(0, 0, -days)
		n, err := repo.PurgeRunsOlderThan(ctx, cutoff)
		if err != nil {
			return "", fmt.Errorf("purge connector_runs: %w", err)
		}
		return fmt.Sprintf("Purged **%d** connector_run row(s) older than %d day(s) (cutoff: %s).",
			n, days, cutoff.Format(time.RFC3339)), nil
	}
}
