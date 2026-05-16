package setup

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/workflow/service"
	"github.com/yogasw/wick/internal/agents/workflow/trigger"
)

const watchInterval = 3 * time.Second

// WatchWorkflows polls <workflowsDir>/*/workflow.yaml every 3 seconds
// and calls HotReload on any slug whose mtime changed since the last
// poll. New files trigger a load; removed files trigger an unregister.
// Blocks until ctx is cancelled — run in a goroutine.
func WatchWorkflows(ctx context.Context, workflowsDir string, svc service.Service, router *trigger.Router, cron *trigger.CronScheduler) {
	mtimes := map[string]time.Time{}

	tick := time.NewTicker(watchInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pollWorkflows(ctx, workflowsDir, svc, router, cron, mtimes)
		}
	}
}

func pollWorkflows(ctx context.Context, workflowsDir string, svc service.Service, router *trigger.Router, cron *trigger.CronScheduler, mtimes map[string]time.Time) {
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		return
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		yamlPath := filepath.Join(workflowsDir, slug, "workflow.yaml")
		info, err := os.Stat(yamlPath)
		if err != nil {
			continue
		}
		seen[slug] = true
		prev, exists := mtimes[slug]
		if !exists || info.ModTime().After(prev) {
			mtimes[slug] = info.ModTime()
			if err := HotReload(ctx, svc, router, cron, slug); err != nil {
				log.Warn().Err(err).Str("slug", slug).Msg("workflow: hot-reload failed")
			} else if exists {
				log.Info().Str("slug", slug).Msg("workflow: hot-reloaded")
			}
		}
	}

	// Unregister slugs whose folder was removed.
	for slug := range mtimes {
		if !seen[slug] {
			delete(mtimes, slug)
			router.Unregister(slug)
			if cron != nil {
				cron.Unsync(slug)
			}
			log.Info().Str("slug", slug).Msg("workflow: unregistered (folder removed)")
		}
	}
}
