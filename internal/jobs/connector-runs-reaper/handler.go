// Package connectorrunsreaper is wick's built-in maintenance job that
// finalizes connector_runs rows wedged in "running". A run is normally
// finalized by the deferred writer in connectors.Service.Execute; a row is only
// left "running" when that never ran — a process crash mid-op, or a plugin call
// that hung past every guard. This job flips such stale rows to "cancelled" so
// the history/tracer UI never shows a run stuck "running" forever.
//
// It complements two other layers: the boot-time reset in
// connectors.Service.Bootstrap (catches crash leftovers at startup) and the
// per-op server-side timeout (prevents most hangs in the first place). This job
// is the RUNTIME net — it reclaims a wedged row without waiting for a restart.
//
// Default cron: every 5 minutes. Default threshold: 10 minutes — safely above
// any legitimate op (the plugin op deadline is 45s and the server-side op
// timeout is minutes), so a still-legitimately-running op is never reclaimed out
// from under itself.
package connectorrunsreaper

import (
	"context"
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"

	"gorm.io/gorm"
)

// Key is the job slug — also the row key in the `jobs` table.
const Key = "connector-runs-reaper"

// defaultThresholdMinutes is the in-code fallback when the Config row is missing
// or non-positive. A run "running" longer than this at sweep time can't be a
// live op (every op has a shorter ceiling), so it's reclaimed.
const defaultThresholdMinutes = 10

// Config is the per-instance tunable: how old (minutes) a "running" row must be
// before it's reclaimed. Kept generous so a legitimate slow op is never killed.
type Config struct {
	ThresholdMinutes int `wick:"default=10;desc=Minutes a run may stay 'running' before the reaper reclaims it as cancelled. Keep above your longest op."`
}

// Register adds the reaper job to the global jobs registry. Must be called from
// BOTH the web server and the worker (same reasoning as the purge job).
func Register(db *gorm.DB) {
	jobs.Register(job.Module{
		Meta: job.Meta{
			Key:         Key,
			Name:        "Connector Runs Reaper",
			Description: "Finalizes connector runs wedged in 'running' (crash/hang leftovers) as cancelled.",
			Icon:        "⏱️",
			DefaultCron: "*/5 * * * *",
			DefaultTags: []tool.DefaultTag{tags.System},
			AutoEnable:  true,
		},
		Configs: entity.StructToConfigs(Config{ThresholdMinutes: defaultThresholdMinutes}),
		Run:     NewRun(db),
	})
}

// NewRun returns a job.RunFunc bound to the given DB handle.
func NewRun(db *gorm.DB) job.RunFunc {
	repo := connectors.NewRepo(db)
	return func(ctx context.Context) (string, error) {
		mins := job.FromContext(ctx).CfgInt("threshold_minutes")
		if mins <= 0 {
			mins = defaultThresholdMinutes
		}
		cutoff := time.Now().Add(-time.Duration(mins) * time.Minute)
		n, err := repo.ResetStuckRuns(ctx, cutoff)
		if err != nil {
			return "", fmt.Errorf("reap stuck connector_runs: %w", err)
		}
		if n == 0 {
			return fmt.Sprintf("No stuck runs (none 'running' older than %d min).", mins), nil
		}
		return fmt.Sprintf("Reclaimed **%d** connector_run row(s) stuck in 'running' longer than %d min.", n, mins), nil
	}
}
