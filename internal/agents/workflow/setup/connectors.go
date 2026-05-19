package setup

import (
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// RegisterLiveConnectors copies every connector module currently
// registered in internal/connectors/ into the workflow connector
// registry. Call after connectors.RegisterBuiltins() has populated
// the global registry — otherwise the workflow registry is empty
// and `type: connector` nodes fail with "module not registered".
func RegisterLiveConnectors(reg *connector.Registry) {
	for _, m := range connectors.All() {
		reg.Register(pkgconnector.Module{
			Meta:        m.Meta,
			Configs:     m.Configs,
			Operations:  m.Operations,
			HealthCheck: m.HealthCheck,
		})
	}
}
