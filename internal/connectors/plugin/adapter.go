package plugin

import (
	"encoding/json"
	"fmt"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// ConnGetter returns a live plugin connection for a connector key. The
// manager's Client method satisfies it; tests pass a fake.
type ConnGetter func(key string) (wickplugin.GRPCConn, error)

// BuildModule wires a parsed connector.Module's operations to gRPC closures
// that dispatch to the plugin subprocess. The host engine (service.Execute)
// calls these closures exactly like in-proc ops — same pattern as custom-MCP.
// The envelope parsing and verification happen in the loader before this is
// called.
func BuildModule(mod connector.Module, getConn ConnGetter) connector.Module {
	key := mod.Meta.Key
	for ci := range mod.Operations {
		for oi := range mod.Operations[ci].Ops {
			opKey := mod.Operations[ci].Ops[oi].Key
			mod.Operations[ci].Ops[oi].Execute = newExecuteClosure(key, opKey, getConn)
		}
	}
	return mod
}

func newExecuteClosure(connKey, opKey string, getConn ConnGetter) connector.ExecuteFunc {
	return func(c *connector.Ctx) (any, error) {
		conn, err := getConn(connKey)
		if err != nil {
			return nil, fmt.Errorf("plugin %q unavailable: %w", connKey, err)
		}
		raw, err := conn.Execute(c.Context(), wickplugin.ExecCall{
			Operation: opKey,
			Input:     c.Inputs(),
			Creds:     c.Configs(),
		})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(raw), nil
	}
}
