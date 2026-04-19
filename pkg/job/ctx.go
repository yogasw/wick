package job

import (
	"context"
	"strconv"
	"strings"
)

// cfgReader is the narrow slice of the configs service that a job run
// needs. The concrete implementation lives in internal/configs — we
// keep the contract here so pkg/job has no import on internal.
type cfgReader interface {
	GetOwned(owner, key string) string
}

// Ctx is the per-run handle wick passes to a job through context. It
// carries the owning job's Key and a view onto the configs service so
// Run() can read its own runtime-editable config without re-plumbing
// the service down from main.
//
// Construction is internal to wick — jobs receive a ready-made Ctx via
// job.FromContext(ctx) inside Run.
type Ctx struct {
	owner string
	cfg   cfgReader
}

type ctxKey struct{}

// NewCtx is used by the manager package to build a Ctx before calling
// Run. Downstream code should never call this — read the existing Ctx
// with FromContext instead.
func NewCtx(owner string, cfg cfgReader) *Ctx {
	return &Ctx{owner: owner, cfg: cfg}
}

// WithCtx attaches a Ctx to a context.Context. The scheduler calls this
// before Run so handlers can recover the Ctx via FromContext.
func WithCtx(parent context.Context, c *Ctx) context.Context {
	return context.WithValue(parent, ctxKey{}, c)
}

// FromContext returns the Ctx attached to ctx, or a no-op Ctx when
// none is present (e.g. unit tests that call Run directly with a bare
// context). A no-op Ctx returns "" for every Cfg read, which matches
// the behavior of a tool that declares no configs.
func FromContext(ctx context.Context) *Ctx {
	if c, ok := ctx.Value(ctxKey{}).(*Ctx); ok && c != nil {
		return c
	}
	return &Ctx{}
}

// Owner returns the job Key this Ctx is scoped to. Useful for logging
// — Cfg already applies the scope for reads.
func (c *Ctx) Owner() string { return c.owner }

// Cfg returns the current value of a config field declared by this
// job. Scoped to the active job's Key — reading another owner's
// config requires CfgOf. Returns "" when the key is not declared or
// the config service is unavailable.
func (c *Ctx) Cfg(key string) string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.GetOwned(c.owner, key)
}

// CfgOf reads a config value from another owner (a tool or another
// job). Intentionally verbose — reserved for cross-owner reads that
// need a neighbor's endpoint or shared identifier.
func (c *Ctx) CfgOf(owner, key string) string {
	if c.cfg == nil {
		return ""
	}
	return c.cfg.GetOwned(owner, key)
}

// CfgInt returns c.Cfg(key) parsed as int. Unparseable or empty values
// return 0.
func (c *Ctx) CfgInt(key string) int {
	n, _ := strconv.Atoi(c.Cfg(key))
	return n
}

// CfgBool returns c.Cfg(key) parsed as bool. "true"/"1"/"yes"/"on"
// (case-insensitive) count as true; anything else is false.
func (c *Ctx) CfgBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(c.Cfg(key))) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
