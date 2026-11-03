// Package sessionconnectorsexpiry is wick's built-in maintenance job that
// deletes session connectors whose user-set expiry passed. A session connector
// normally dies with its session's activity (the idle sweeper in
// internal/agents/sessionworkspace); one the user pinned with an expiry
// survives idleness and is reclaimed here instead, once its time is up.
//
// Default cron: every 5 minutes. Between the expiry and the next tick the
// instance is already hidden from list/get/execute, so the tick only reclaims
// the file — the lifetime the user chose is exact either way.
package sessionconnectorsexpiry

import (
	"context"
	"fmt"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

// Key is the job slug — also the row key in the `jobs` table.
const Key = "session-connectors-expiry"

// Register adds the job to the global jobs registry. Called from both the web
// server and the worker, like the other built-in maintenance jobs; the
// registry dedupes on Key when both run in one process.
func Register() {
	jobs.Register(job.Module{
		Meta: job.Meta{
			Key:         Key,
			Name:        "Session Connectors Expiry",
			Description: "Deletes session connectors whose expiry (set by the user) has passed.",
			Icon:        "⏳",
			DefaultCron: "*/5 * * * *",
			DefaultTags: []tool.DefaultTag{tags.System},
			AutoEnable:  true,
		},
		Run: Run,
	})
}

// Run reaps every expired session connector on this host's agents tree.
func Run(ctx context.Context) (string, error) {
	layout := agentconfig.NewLayout(agentconfig.ResolveBaseDir(agentconfig.StorageConfig{}))
	n, err := sessionworkspace.ReapExpired(layout, time.Now())
	if err != nil {
		return "", fmt.Errorf("reap expired session connectors: %w", err)
	}
	return fmt.Sprintf("Deleted **%d** expired session connector(s).", n), nil
}
