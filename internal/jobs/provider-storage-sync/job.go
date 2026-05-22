// Package providerstoragesync is a background job that backs up all enabled
// provider-storage sources from disk to DB on a fixed interval.
// The interval is read from the provider-storage config at each tick.
package providerstoragesync

import (
	"context"
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/agents/providersync"
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
			AutoEnable:  true,
		},
		Run: newRun(mgr),
	})
}

func newRun(mgr *providersync.Manager) job.RunFunc {
	return func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()

		sources, err := mgr.ListSources(ctx)
		if err != nil {
			return "", err
		}
		synced := 0
		for _, src := range sources {
			if ctx.Err() != nil {
				return "", fmt.Errorf("sync timed out after 60s: synced %d/%d source(s)", synced, len(sources))
			}
			if !src.Enabled {
				continue
			}
			if err := mgr.SyncOne(ctx, providersync.SourceToInstance(src)); err != nil {
				continue
			}
			synced++
		}
		return fmt.Sprintf("synced %d source(s)", synced), nil
	}
}
