// Package providerstorageretention purges expired provider_storage file rows
// and old history rows on a daily schedule.
package providerstorageretention

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/providersync"
	"github.com/yogasw/wick/internal/jobs"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"
)

const Key = "provider-storage-retention"

func Register(mgr *providersync.Manager) {
	jobs.Register(job.Module{
		Meta: job.Meta{
			Key:         Key,
			Name:        "Provider Storage Retention",
			Description: "Purges expired provider storage file rows and history older than 30 days.",
			Icon:        "🧹",
			DefaultCron: "0 3 * * *",
			DefaultTags: []tool.DefaultTag{tags.System},
			AutoEnable:  true,
		},
		Run: func(ctx context.Context) (string, error) {
			mgr.RunRetention(ctx)
			return fmt.Sprintf("retention run complete"), nil
		},
	})
}
