// Command jobecho is a test job plugin: its `mode` config picks the outcome
// (ok / fail / sleep) so the host adapter's spawn → Run → kill path, error
// propagation and timeout can be exercised end to end.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

type Config struct {
	Mode    string `wick:"desc=ok, fail or sleep"`
	Message string `wick:"desc=Echoed back in the result"`
}

func run(ctx context.Context) (string, error) {
	c := job.FromContext(ctx)
	job.Logf(ctx, "pid %d starting", os.Getpid())
	switch c.Cfg("mode") {
	case "fail":
		return "", errors.New("boom")
	case "sleep":
		select {
		case <-time.After(30 * time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	job.Logf(ctx, "echo %s", c.Cfg("message"))
	return fmt.Sprintf("done: %s (pid %d)", c.Cfg("message"), os.Getpid()), nil
}

func main() {
	wickplugin.ServeJob(job.Module{
		Meta: job.Meta{
			Key:         "jobecho",
			Name:        "Job Echo",
			Description: "Test job plugin.",
			DefaultCron: "*/5 * * * *",
		},
		Configs: entity.StructToConfigs(Config{}),
		Run:     run,
	})
}
