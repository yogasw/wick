package setup

import (
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/connectors"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// RegisterLiveConnectors keeps the workflow connector registry in sync
// with the global connector registry — every module already registered
// is copied immediately, and every future connectors.Register call also
// flows through. The previous one-shot snapshot variant depended on
// boot ordering: any connector registered after this call (wfconn,
// wickmanager, notifications — all late-bound because they need runtime
// Deps) silently went missing from /api/workflows/palette and
// /workflows/api/registry. The observer hook removes that footgun.
//
// Safe to call once per workflow registry instance; subscriptions are
// not removable, so calling twice for the same registry would double-
// fire on future Register events. Today the call sites in api/server.go
// (HTTP) and api/server_mcp.go (stdio) each touch their own registry.
func RegisterLiveConnectors(reg *connector.Registry) {
	connectors.OnRegister(func(m pkgconnector.Module) {
		reg.Register(pkgconnector.Module{
			Meta:        m.Meta,
			Configs:     m.Configs,
			Operations:  m.Operations,
			HealthCheck: m.HealthCheck,
		})
	})
}
