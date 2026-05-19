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
// and calls HotReload on any id whose mtime changed since the last
// poll. New files trigger a load; removed files trigger an unregister.
// Blocks until ctx is cancelled — run in a goroutine.
func WatchWorkflows(ctx context.Context, workflowsDir string, svc service.Service, router *trigger.Router, cron *trigger.CronScheduler, schedAt *trigger.ScheduleAtScheduler) {
	mtimes := map[string]time.Time{}

	tick := time.NewTicker(watchInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pollWorkflows(ctx, workflowsDir, svc, router, cron, schedAt, mtimes)
		}
	}
}

func pollWorkflows(ctx context.Context, workflowsDir string, svc service.Service, router *trigger.Router, cron *trigger.CronScheduler, schedAt *trigger.ScheduleAtScheduler, mtimes map[string]time.Time) {
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		return
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		yamlPath := filepath.Join(workflowsDir, id, "workflow.yaml")
		info, err := os.Stat(yamlPath)
		if err != nil {
			continue
		}
		seen[id] = true
		prev, exists := mtimes[id]
		if !exists || info.ModTime().After(prev) {
			mtimes[id] = info.ModTime()
			if err := HotReload(ctx, svc, router, cron, schedAt, id); err != nil {
				log.Warn().Err(err).Str("wf_id", id).Msg("workflow: hot-reload failed")
			} else if exists {
				log.Info().Str("wf_id", id).Msg("workflow: hot-reloaded")
			}
		}
	}

	// Unregister ids whose folder was removed.
	for id := range mtimes {
		if !seen[id] {
			delete(mtimes, id)
			router.Unregister(id)
			if cron != nil {
				cron.Unsync(id)
			}
			if schedAt != nil {
				schedAt.Unsync(id)
			}
			log.Info().Str("wf_id", id).Msg("workflow: unregistered (folder removed)")
		}
	}
}
