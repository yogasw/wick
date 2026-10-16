package plugin

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	connectors "github.com/yogasw/wick/internal/connectors"
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
	if mod.HasHealthCheck {
		mod.HealthCheck = newHealthCheckClosure(key, getConn)
	}
	return mod
}

// newHealthCheckClosure runs the plugin module's HealthCheck hook in the
// plugin. Only wired when the manifest says the hook exists; a plugin that
// still answers no_health_check maps to connectors.ErrNoHealthCheck, so
// "test setup" falls back to running an operation as before.
func newHealthCheckClosure(connKey string, getConn ConnGetter) connector.HealthCheckFunc {
	return func(c *connector.Ctx) ([]connector.OpHealth, error) {
		lease, err := getConn(connKey)
		if err != nil {
			return nil, fmt.Errorf("plugin %q unavailable: %w", connKey, err)
		}
		defer lease.Release()
		res, err := lease.Conn.ExecuteStream(c.Context(), wickplugin.ExecCall{
			HealthCheck: true,
			Creds:       c.Configs(),
			InstanceID:  c.InstanceID(),
		})
		if err != nil {
			if errors.Is(err, wickplugin.ErrPluginOp) && strings.Contains(err.Error(), "["+wickplugin.CodeNoHealthCheck+"]") {
				return nil, connectors.ErrNoHealthCheck
			}
			return nil, err
		}
		var report []connector.OpHealth
		if err := json.Unmarshal(res.JSON, &report); err != nil {
			return nil, fmt.Errorf("decode health report: %w", err)
		}
		return report, nil
	}
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
			RequestID:    c.RequestID(),
			SessionID:    c.SessionID(),
			InstanceID:   c.InstanceID(),
			CallerUserID: c.CallerUserID(),
			AccountID:    c.AccountID(),
			OwnerBotID:   c.OwnerBotID(),
			RawInput:     c.RawInput(),
			Progress:     c.ReportProgress,
		})
		if err != nil {
			// Register what the op asked to mask even when it failed, so the
			// host's sweep scrubs those values from the error message too.
			registerPluginMasks(c, res)
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

// registerPluginMasks hands the values a failed plugin op passed to c.Mask /
// c.MaskIgnoreCase to the host masker without masking any data, so they join
// the post-execute sweep that scrubs the error message.
func registerPluginMasks(c *connector.Ctx, res wickplugin.ExecResult) {
	if len(res.Mask) > 0 {
		c.Mask("", res.Mask)
	}
	if len(res.MaskIgnoreCase) > 0 {
		c.MaskIgnoreCase("", res.MaskIgnoreCase)
	}
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
