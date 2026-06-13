// Package sessionconfigpurge is wick's maintenance job that removes
// stale per-session workspaces (sessions/<id>/workspace.json) by file
// age. A workspace holds a session's ephemeral connector instances; it
// already dies with its session, so this is the TTL backstop so a
// long-lived session doesn't hold an instance (e.g. a staging
// credential) forever.
//
// File-based, so unlike connector-runs-purge it needs no DB handle —
// it resolves the agents layout from the platform default base dir at
// run time. Registered from BOTH the worker (scheduler tick) and the
// web server (so /admin/jobs sees the row).
//
// Default cron: daily. Default retention: 30 days. Purge keys off the
// workspace file's mtime, and execute only READS the file (never writes),
// so a generous window keeps a long-lived session from losing an
// instance it configured early and used all day. Both editable
// per-instance from the manager UI.
package sessionconfigpurge

import (
	"context"
	"fmt"
	"os"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

// Key is the job slug — also the row key in the `jobs` table.
const Key = "session-config-purge"

// defaultRetentionHours is the in-code fallback when the Config row is
// missing or non-positive. 30 days — instances are throwaway but a
// long-lived session must never lose one mid-use (mtime-based purge,
// reads don't bump mtime), so the window is deliberately generous.
const defaultRetentionHours = 24 * 30

// Register adds the purge job to the global jobs registry. Call once
// per process, from BOTH the web server and the worker (mirrors
// connector-runs-purge). No DB handle needed — the run resolves the
// agents layout itself.
func Register() {
	jobs.Register(job.Module{
		Meta: job.Meta{
			Key:         Key,
			Name:        "Session Workspace Purge",
			Description: "Daily cleanup of per-session connector workspaces (ephemeral instances) older than the retention window.",
			Icon:        "🧹",
			DefaultCron: "15 3 * * *",
			DefaultTags: []tool.DefaultTag{tags.System},
			AutoEnable:  true,
		},
		Configs: entity.StructToConfigs(Config{RetentionHours: defaultRetentionHours}),
		Run:     NewRun(),
	})
}

// NewRun returns the job.RunFunc. The agents layout is resolved at run
// time from the platform default base dir, so the job needs nothing
// wired in at registration.
func NewRun() job.RunFunc {
	return func(ctx context.Context) (string, error) {
		hours := job.FromContext(ctx).CfgInt("retention_hours")
		if hours <= 0 {
			hours = defaultRetentionHours
		}
		cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)
		layout := agentconfig.NewLayout(agentconfig.ResolveBaseDir(agentconfig.StorageConfig{}))

		entries, err := os.ReadDir(layout.SessionsDir())
		if err != nil {
			if os.IsNotExist(err) {
				return "No sessions directory — nothing to purge.", nil
			}
			return "", fmt.Errorf("read sessions dir: %w", err)
		}
		removed := 0
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := layout.SessionWorkspace(e.Name())
			info, statErr := os.Stat(path)
			if statErr != nil {
				continue // no workspace file for this session
			}
			if info.ModTime().Before(cutoff) {
				if rmErr := os.Remove(path); rmErr == nil {
					removed++
				}
			}
		}
		return fmt.Sprintf("Purged **%d** stale session workspace file(s) older than %dh (cutoff: %s).",
			removed, hours, cutoff.Format(time.RFC3339)), nil
	}
}
