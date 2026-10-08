package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/yogasw/wick/pkg/connector"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// ConnGetter returns a lease on a live plugin connection for a connector key.
// The manager's Client method satisfies it; tests pass a fake.
type ConnGetter func(key string) (*Lease, error)

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
		lease, err := getConn(connKey)
		if err != nil {
			return nil, fmt.Errorf("plugin %q unavailable: %w", connKey, err)
		}
		defer lease.Release()
		res, err := lease.Conn.ExecuteStream(c.Context(), wickplugin.ExecCall{
			Operation:    opKey,
			Input:        c.Inputs(),
			Creds:        c.Configs(),
			InstanceID:   c.InstanceID(),
			CallerUserID: c.CallerUserID(),
		})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(maskPluginResult(c, res)), nil
	}
}

// maskPluginResult applies the c.Mask / c.MaskIgnoreCase calls the plugin
// made. The plugin cannot encrypt (it holds no key), so it only reports the
// values; masking them here through the host Ctx turns them into wick_enc_
// tokens and registers them for the post-execute sweep, the same as for an
// in-process connector. Strings are masked after decoding so a value that
// JSON escapes (quotes, backslashes, non-ASCII) still matches.
func maskPluginResult(c *connector.Ctx, res wickplugin.ExecResult) []byte {
	if len(res.Mask) == 0 && len(res.MaskIgnoreCase) == 0 {
		return res.JSON
	}
	maskStr := func(s string) string {
		if len(res.Mask) > 0 {
			s = c.Mask(s, res.Mask)
		}
		if len(res.MaskIgnoreCase) > 0 {
			s = c.MaskIgnoreCase(s, res.MaskIgnoreCase)
		}
		return s
	}
	dec := json.NewDecoder(bytes.NewReader(res.JSON))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []byte(maskStr(string(res.JSON)))
	}
	out, err := json.Marshal(maskJSONValue(v, maskStr))
	if err != nil {
		return []byte(maskStr(string(res.JSON)))
	}
	return out
}

func maskJSONValue(v any, maskStr func(string) string) any {
	switch t := v.(type) {
	case string:
		return maskStr(t)
	case []any:
		for i := range t {
			t[i] = maskJSONValue(t[i], maskStr)
		}
		return t
	case map[string]any:
		masked := make(map[string]any, len(t))
		for k, val := range t {
			masked[maskStr(k)] = maskJSONValue(val, maskStr)
		}
		return masked
	default:
		return v
	}
}
