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
//  3. Register here inside RegisterBuiltins() (core wick lab) or in
//     the downstream project's main.go via app.RegisterJob.
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
// app.RegisterJob. All() returns only these — wick's own built-in
// jobs are opt-in via RegisterBuiltins (called by cmd/lab only).
var extra []job.Module

// Register appends a fully-resolved Module to the registry. Called
// from app.RegisterJob; do not call directly from app code.
func Register(m job.Module) {
	extra = append(extra, m)
}

// RegisterBuiltins appends wick's own in-house jobs to the registry.
// Intended for the wick lab binary (cmd/lab), not downstream projects.
func RegisterBuiltins() {
	extra = append(extra,
		job.Module{
			Meta: job.Meta{
				Key:         "sample-post",
				Name:        "Sample Post",
				Description: "Fetches a post from JSONPlaceholder API as a demo job.",
				Icon:        "📝",
				DefaultCron: "0 * * * *",
			},
			Configs: entity.StructToConfigs(samplepost.Config{}),
			Run:     samplepost.Run,
		},
		job.Module{
			Meta: job.Meta{
				Key:         "sample-post-typicode",
				Name:        "Sample Post (Typicode Mirror)",
				Description: "Second instance of sample-post hitting a different base URL — same logic, different config.",
				Icon:        "🪞",
				DefaultCron: "*/15 * * * *",
			},
			Configs: entity.StructToConfigs(samplepost.Config{}),
			Run:     samplepost.Run,
		},
	)
}

// All returns every registered background job in registration order.
func All() []job.Module {
	return extra
}
