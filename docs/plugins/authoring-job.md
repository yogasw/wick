# Authoring a job plugin

A job plugin wraps a `job.Module` (`Meta`, `Configs`, `Run`). wick spawns the
binary when the job fires — on its cron or from *Run now* — calls `Run` once
over gRPC, records the result in the run history, and kills the process.

```go
package main

import (
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	wickplugin "github.com/yogasw/wick/pkg/plugin"

	autogetdata "example.com/yourjobs/auto-get-data"
)

func main() {
	wickplugin.ServeJob(job.Module{
		Meta: job.Meta{
			Key:         "auto_get_data",
			Name:        "Auto Get Data",
			DefaultCron: "*/30 * * * *",
		},
		Configs: entity.StructToConfigs(autogetdata.Config{}),
		Run:     autogetdata.Run, // func(ctx context.Context) (string, error)
	})
}
```

`Run` reads its config exactly as a built-in job does; the host sends the
current config with each run.

## Template, build, test

```bash
cp -r job/_template job/<key> && echo 0.1.0 > job/<key>/VERSION
wick plugin build --kind job <key> --target linux/amd64
go build -o /tmp/<key> ./job/<key> && /tmp/<key> --dump-manifest   # kind=job
<app> plugin install bin/<key>-0.1.0-linux-amd64.zip
```

The job appears on the Jobs page; set its schedule there and use *Run now* to
test.

## Notes

- Nothing survives between runs inside the process. Keep state in a file or
  an external store.
- The plugin environment is scrubbed; a job that relied on host environment
  variables must get them through `Configs` instead.
