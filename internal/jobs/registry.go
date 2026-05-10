// Package jobs is the single place to register all background job
// modules. Jobs are scheduled workers (cron-like) — distinct from tools
// under internal/tools/, which are user-facing UI modules.
//
// Shape of a job module (see the `job-module` skill for full rules):
//
//  1. Package under internal/jobs/<job-name>/ exposing a top-level
//     `func Run(ctx context.Context) (string, error)` plus, optionally,
//     a typed Config struct with `wick:"..."` tags.
//  2. No NewJob(), no Meta() method, no Handler struct — meta is
//     declared by the caller of app.RegisterJob and carried by wick.
//  3. Register here inside RegisterBuiltins() (default-on for every wick
//     app) or RegisterLabSamples() (cmd/lab only), or in the downstream
//     project's main.go via app.RegisterJob.
//
// The manager service reads this list at startup, syncs each job with
// the jobs table, and the cron server uses the DB-stored schedule to
// run them.
package jobs

import (
	samplepost "github.com/yogasw/wick/internal/jobs/sample-post"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
)

// extra holds jobs registered by downstream projects via
// app.RegisterJob, plus the modules added by RegisterBuiltins /
// RegisterLabSamples. All() returns this slice verbatim.
var extra []job.Module

// Register appends a fully-resolved Module to the registry. Called
// from app.RegisterJob; do not call directly from app code.
//
// Idempotent on Meta.Key — calling twice with the same key is a no-op
// on the second call. This is needed because the system tray runs both
// the HTTP server and the background worker in the same process, and
// each of those independently registers built-in jobs (e.g.,
// connector-runs-purge) that need a DB handle.
func Register(m job.Module) {
	for _, existing := range extra {
		if existing.Meta.Key == m.Meta.Key {
			return
		}
	}
	extra = append(extra, m)
}

// RegisterBuiltins seeds in-house jobs every downstream wick app gets
// by default. Currently empty — the only built-in job
// (connector-runs-purge) needs a *gorm.DB handle and is registered
// inline in server.go / worker.go via connectorrunspurge.Register(db),
// not via this static seed. Kept as a stable extension point so future
// no-deps built-in jobs have an obvious home and the call site in
// server.go / worker.go stays uniform with tools / connectors.
func RegisterBuiltins() {}

// RegisterLabSamples seeds the demo-only jobs shipped with the cmd/lab
// binary — currently sample-post and a second instance to demonstrate
// the per-key config split.
func RegisterLabSamples() {
	Register(job.Module{
		Meta: job.Meta{
			Key:         "sample-post",
			Name:        "Sample Post",
			Description: "Fetches a post from JSONPlaceholder API as a demo job.",
			Icon:        "📝",
			DefaultCron: "0 * * * *",
		},
		Configs: entity.StructToConfigs(samplepost.Config{}),
		Run:     samplepost.Run,
	})
	Register(job.Module{
		Meta: job.Meta{
			Key:         "sample-post-typicode",
			Name:        "Sample Post (Typicode Mirror)",
			Description: "Second instance of sample-post hitting a different base URL — same logic, different config.",
			Icon:        "🪞",
			DefaultCron: "*/15 * * * *",
		},
		Configs: entity.StructToConfigs(samplepost.Config{}),
		Run:     samplepost.Run,
	})
}

// All returns every registered background job in registration order.
func All() []job.Module {
	return extra
}
