// Command example_heartbeat is a tiny job plugin that proves the job plugin
// path end to end: it logs a couple of progress lines and returns a markdown
// heartbeat with its own pid, so the run history shows that every run gets a
// fresh process.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type Config struct {
	Greeting string `wick:"desc=Text included in every heartbeat. Example: hello from a plugin"`
}

func run(ctx context.Context) (string, error) {
	greeting := job.FromContext(ctx).Cfg("greeting")
	if greeting == "" {
		greeting = "alive"
	}
	job.Logf(ctx, "heartbeat start pid=%d", os.Getpid())
	job.Logf(ctx, "go %s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return fmt.Sprintf("💓 **%s** at %s (pid %d)", greeting, time.Now().Format(time.RFC3339), os.Getpid()), nil
}

func main() {
	wickplugin.ServeJob(job.Module{
		Meta: job.Meta{
			Key:         "example_heartbeat",
			Name:        "Example Heartbeat",
			Description: "Example job plugin: logs progress and returns a heartbeat.",
			Icon:        "💓",
			DefaultCron: "*/15 * * * *",
		},
		Configs: entity.StructToConfigs(Config{}),
		Run:     run,
	})
}
