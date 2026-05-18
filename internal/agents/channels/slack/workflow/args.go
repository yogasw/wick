package workflow

import (
	"encoding/json"
	"fmt"
)

// argString reads a required string arg, returning "" + error if
// missing or wrong type. Use for required Slack fields like channel,
// trigger_id, callback_id.
func argString(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required arg %q", key)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("arg %q must be a non-empty string", key)
	}
	return s, nil
}

// argStringOpt reads an optional string arg, returning "" when missing
// or wrong type — no error. Use for optional fields like thread_ts.
func argStringOpt(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// argBool reads an optional bool, defaulting to def when missing or
// wrong type.
func argBool(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

// argJSON decodes a JSON-encoded string arg into out. Slack's view +
// block kit args travel as JSON blobs in workflow YAML so the operator
// can paste from the Block Kit Builder directly. Returns an error if
// the field is missing, not a string, or invalid JSON.
func argJSON(args map[string]any, key string, out any) error {
	val, ok := args[key]
	if !ok || val == nil {
		return fmt.Errorf("arg %q must be a non-empty string", key)
	}
	// Accept either a JSON string or a pre-decoded map/slice (e.g. from YAML).
	var raw string
	switch v := val.(type) {
	case string:
		if v == "" {
			return fmt.Errorf("arg %q must be a non-empty string", key)
		}
		raw = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("arg %q: cannot marshal to JSON: %w", key, err)
		}
		raw = string(b)
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("arg %q: invalid JSON: %w", key, err)
	}
	return nil
}

// argJSONOpt is argJSON for an optional field. Returns nil (no error,
// no decode) when the field is missing.
func argJSONOpt(args map[string]any, key string, out any) error {
	if _, ok := args[key]; !ok {
		return nil
	}
	return argJSON(args, key, out)
}
