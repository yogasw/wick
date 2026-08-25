# Job Module

Jobs live in `jobs/<name>/` and mount at `/jobs/{key}` — shows schedule, run history, and a Run Now button. The module only needs a top-level `Run` func.

::: info See also
For an example of a System-tagged job (auto-enabled, code-managed), see [Connector Runs Purge](./connector-runs-purge) — it's the built-in retention worker for connector audit logs.
:::

![Job Detail](/screenshots/job-detail.png)
*Job page — schedule info, total runs, last run time, and full run history with results.*

![Job Settings](/screenshots/job-settings.png)
*Job settings — cron expression, max runs, enable/disable, and runtime config.*

## File Structure

```
jobs/my-job/
├── handler.go    # top-level Run func
├── config.go     # typed Config struct (if job has knobs)
├── service.go    # orchestration / business logic
└── repo.go       # external I/O — DB, HTTP (optional)
```

## Register in main.go

```go
app.RegisterJob(
    job.Meta{
        Key:         "my-job",
        Name:        "My Job",
        Description: "Fetch remote data on a schedule.",
        Icon:        "🌐",
        DefaultCron: "*/30 * * * *",
        DefaultTags: []tool.DefaultTag{tags.Job},
    },
    myjob.Config{},
    myjob.Run,
)
```

For jobs with no runtime config:

```go
app.RegisterJobNoConfig(meta, myjob.Run)
```

### job.Meta fields

| Field | Description |
|-------|-------------|
| `Key` | Unique slug, kebab-case |
| `Name` | Display name |
| `Description` | Card subtitle |
| `Icon` | Emoji shown on card |
| `DefaultCron` | Initial cron expression — admin can edit later |
| `DefaultTags` | Slice of `tool.DefaultTag` from `tags/defaults.go` |

## Run Function

```go
package myjob

import (
    "context"
    "errors"
    "fmt"

    "github.com/yogasw/wick/pkg/job"
)

func Run(ctx context.Context) (string, error) {
    c := job.FromContext(ctx)

    url := c.Cfg("url")
    if url == "" {
        return "", errors.New("url not configured — set it from /manager/jobs")
    }

    n, err := fetchRemote(ctx, url)
    if err != nil {
        return "", err
    }

    return fmt.Sprintf("fetched %d bytes from %s", n, url), nil
}
```

- **Returned string** → stored as the run result summary (shown in history)
- **Non-nil error** → marks the run as failed

## Runtime Config

Same `wick:"..."` tag grammar as tools — see the **[Config Tag Reference](/reference/config-tags)** for the full widget table and all flags.

```go
package myjob

type Config struct {
    URL    string `wick:"desc=Endpoint to fetch.;url;required"`
    APIKey string `wick:"desc=Bearer token.;secret"`
    Limit  int    `wick:"desc=Max records per run.;number"`
}
```

Read inside `Run`:

```go
c := job.FromContext(ctx)
url    := c.Cfg("url")
apiKey := c.Cfg("api_key")
limit  := c.CfgInt("limit")
```

## Worker vs Web

Both processes share the same `configs` table. Admin edits take effect on the next cron tick.

| Process | Command | Responsibility |
|---------|---------|----------------|
| Web | `go run . server` | Mounts `/jobs/` and `/manager/jobs/` pages |
| Worker | `go run . worker` | Runs cron ticker, invokes `Run` on schedule |

`go run . dev` starts only the web process. Run `go run . worker` in a second terminal for job execution during development.
