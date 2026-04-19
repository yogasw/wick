// Package job defines the public contract every background job module
// must implement. Jobs are stateless top-level RunFuncs — one call to
// app.RegisterJob binds a Meta, a typed Config value, and a RunFunc.
// The RunFunc receives a context.Context that carries a *Ctx (via
// FromContext) so Run can read its own runtime-editable config without
// any extra plumbing.
package job

import (
	"context"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Meta holds the static metadata for a job instance. Downstream code
// passes one Meta per app.RegisterJob call — duplicating a module with
// a different Key + Meta registers a second scheduled instance backed
// by the same RunFunc.
type Meta struct {
	Key         string
	Name        string
	Description string
	Icon        string
	DefaultCron string
	// DefaultTags works the same as tool.Tool.DefaultTags — seeded on
	// startup, linked to the job's path, and used for tag-based access
	// control in the admin UI.
	DefaultTags []tool.DefaultTag
}

// RunFunc is the job-side run signature. It executes the job and
// returns a markdown-formatted result string. An empty string means
// "no output to display". Errors should be returned via the error
// value, not embedded in the result.
//
// ctx carries a *Ctx (via FromContext) so Run can read this instance's
// runtime config, e.g. job.FromContext(ctx).Cfg("endpoint"). When the
// job needs process-wide state (a DB handle, an HTTP client, a cache),
// wrap RunFunc in a factory: func NewRun(db *sql.DB) job.RunFunc { ... }.
type RunFunc func(ctx context.Context) (string, error)

// Module is the internal, fully-resolved registration record wick
// keeps for every job. It is produced by app.RegisterJob — the Meta,
// any configs reflected from the paired typed struct, and the RunFunc
// itself. Downstream code does not construct Module directly.
type Module struct {
	Meta    Meta
	Configs []entity.Config
	Run     RunFunc
}
