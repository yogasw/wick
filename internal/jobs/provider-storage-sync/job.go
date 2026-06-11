// Package providerstoragesync is a background job that backs up all enabled
// provider-storage sources from disk to DB on a fixed interval.
// The interval is read from the provider-storage config at each tick.
package providerstoragesync

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

const Key = "provider-storage-sync"

func Register(mgr *providersync.Manager) {
	jobs.Register(job.Module{
		Meta: job.Meta{
			Key:         Key,
			Name:        "Provider Storage Sync",
			Description: "Backs up enabled provider config files from disk to DB.",
			Icon:        "💾",
			DefaultCron: "*/1 * * * *",
			DefaultTags: []tool.DefaultTag{tags.System},
			AutoEnable:  false,
		},
		Configs: []entity.Config{
			WatcherStatus,
			WatcherDebounceMs,
		},
		Run: newRun(mgr),
	})
}

func newRun(mgr *providersync.Manager) job.RunFunc {
	return func(ctx context.Context) (string, error) {
		if os.Getenv("WICK_PROVIDERSYNC_DISABLE") == "true" {
			log.Ctx(ctx).Info().Bool("env_disable", true).
				Msg("providersync: sync run skipped — WICK_PROVIDERSYNC_DISABLE=true on this instance")
			return "skipped (WICK_PROVIDERSYNC_DISABLE=true)", nil
		}
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		// Reconcile watcher lifecycle against current config every tick.
		// Cheap: EnsureWatcher is idempotent (returns immediately when
		// already running) and StopWatcher is a no-op when no watcher
		// exists. This is how the UI toggle in the Settings page
		// propagates without restarting the server — flip the cfg row,
		// the next tick brings the watcher state in line. Worst-case
		// lag is one cron period; the watcher itself does NOT depend on
		// this tick to deliver events.
		cfg := job.FromContext(ctx)
		if cfg.CfgBool(CfgWatcherStatus) {
			debounce := cfg.CfgInt(CfgWatcherDebounceMs)
			if err := mgr.EnsureWatcher(ctx, debounce); err != nil {
				return "", fmt.Errorf("watcher start: %w", err)
			}
		} else {
			mgr.StopWatcher()
		}

		sources, err := mgr.ListSources(ctx)
		if err != nil {
			return "", err
		}
		sources_run := 0
		total_changed, total_skipped := 0, 0
		for _, src := range sources {
			if ctx.Err() != nil {
				return "", fmt.Errorf("sync timed out: %d/%d source(s) done, %d file(s) changed", sources_run, len(sources), total_changed)
			}
			if !src.Enabled {
				continue
			}
			changed, skipped, err := mgr.SyncOne(ctx, providersync.SourceToInstance(src))
			if err != nil {
				continue
			}
			sources_run++
			total_changed += changed
			total_skipped += skipped
		}
		return fmt.Sprintf("%d source(s): %d file(s) changed, %d skipped", sources_run, total_changed, total_skipped), nil
	}
}
