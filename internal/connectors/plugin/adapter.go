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

// BuildModule parses a plugin.json manifest into a connector.Module whose
// every Operation.Execute is a closure that dispatches to the plugin
// subprocess over gRPC. The host engine (service.Execute) calls these
// closures exactly like in-proc ops — same pattern as custom-MCP.
func BuildModule(manifest []byte, getConn ConnGetter) (connector.Module, error) {
	var mod connector.Module
	if err := json.Unmarshal(manifest, &mod); err != nil {
		return connector.Module{}, fmt.Errorf("parse manifest: %w", err)
	}
	if mod.Meta.Key == "" {
		return connector.Module{}, fmt.Errorf("manifest missing meta.key")
	}
	key := mod.Meta.Key
	for ci := range mod.Operations {
		for oi := range mod.Operations[ci].Ops {
			opKey := mod.Operations[ci].Ops[oi].Key
			mod.Operations[ci].Ops[oi].Execute = newExecuteClosure(key, opKey, getConn)
		}
	}
	return mod, nil
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
