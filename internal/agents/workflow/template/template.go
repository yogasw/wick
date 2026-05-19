// Package template renders Go text/template strings against a
// workflow.RenderCtx. Strict missing-key handling so typos surface as
// errors instead of `<no value>`. Used by every executor that needs to
// interpolate {{.Event.X}} / {{.Node.X.Y}} / {{.Env.X}} / {{.Secret.X}}.
package template

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	gotemplate "text/template"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// Render parses + executes a Go template with strict missing-key
// handling.
//
// Secret leak guard: `{{.Env.X}}` looks up a secret-tagged key →
// error. Use `{{.Secret.X}}` explicitly.
func Render(tmpl string, ctx workflow.RenderCtx) (string, error) {
	if tmpl == "" {
		return "", nil
	}
	tmpl = normalizeEventPaths(tmpl)
	t := gotemplate.New("node").Funcs(BuiltinFuncs).Option("missingkey=error")
	parsed, err := t.Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}
	var buf bytes.Buffer
	if err := parsed.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	return buf.String(), nil
}

// eventFieldAliases maps the JSON-tag lowercase form of the
// workflow.Event top-level fields back to their Go field names. The
// editor's INPUT pane renders JSON (lowercase keys) and earlier
// drag-emit shipped templates like `{{.Event.payload.x}}`, which Go
// text/template rejects because struct field lookup is case-sensitive
// against the Go name (`Payload`). normalizeEventPaths rewrites any
// `.Event.<lower>` segment to `.Event.<Capital>` so both the legacy
// stored templates and the new drag-emit canonical form evaluate.
//
// Only the immediate next segment after `.Event.` is rewritten —
// deeper paths (`.Event.Payload.channel_id`) are map-key lookups and
// keep their JSON case.
var eventFieldAliases = map[string]string{
	"type":    "Type",
	"subtype": "Subtype",
	"channel": "Channel",
	"at":      "At",
	"payload": "Payload",
}

func normalizeEventPaths(tmpl string) string {
	if !strings.Contains(tmpl, ".Event.") {
		return tmpl
	}
	out := tmpl
	for lower, canon := range eventFieldAliases {
		if lower == canon {
			continue
		}
		out = strings.ReplaceAll(out, ".Event."+lower, ".Event."+canon)
	}
	return out
}

// RenderInto recursively renders string values inside maps/slices.
// Used for node `args:` maps and HTTP headers/query.
func RenderInto(v any, ctx workflow.RenderCtx) (any, error) {
	switch x := v.(type) {
	case string:
		return Render(x, ctx)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			rv, err := RenderInto(val, ctx)
			if err != nil {
				return nil, fmt.Errorf("at key %q: %w", k, err)
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			rv, err := RenderInto(val, ctx)
			if err != nil {
				return nil, fmt.Errorf("at index %d: %w", i, err)
			}
			out[i] = rv
		}
		return out, nil
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			rv, err := Render(val, ctx)
			if err != nil {
				return nil, fmt.Errorf("at key %q: %w", k, err)
			}
			out[k] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

// BuiltinFuncDocs is the single source of truth for available template
// functions. Key = "funcname signature", Value = description.
// Exposed via workflow_workspace so AI always sees the up-to-date list.
var BuiltinFuncDocs = map[string]string{
	"truncate n str": "truncate str to n chars, appends '…'",
	"upper str":      "uppercase",
	"lower str":      "lowercase",
	"trim str":       "trim whitespace",
	"default d v":    "return v if non-empty, else d",
	"toJSON v":       "marshal any value to JSON string — safe for body: fields (aliases: toJson, tojson)",
	"fromJSON s":     "parse JSON string to map/slice/scalar — use to read fields out of stringified JSON (aliases: fromJson, fromjson)",
	"jsonEscape str": "escape string for embedding inside a JSON string literal",
	"now format":     "current UTC time — format uses Go ref time e.g. '2006-01-02T15:04:05Z07:00'",
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// fromJSON parses a JSON string into a generic value (map/slice/scalar)
// so templates can read fields out of stringified JSON payloads. Common
// case: an agent node returns `text` as a JSON string and a downstream
// channel/http node needs to pull individual fields out for the body.
func fromJSON(s string) (any, error) {
	if s == "" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("fromJSON: %w", err)
	}
	return v, nil
}

// BuiltinFuncs are convenience template funcs available in every Render.
var BuiltinFuncs = gotemplate.FuncMap{
	"truncate": func(n int, s string) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"trim":  strings.TrimSpace,
	"default": func(d, v any) any {
		if v == nil || v == "" {
			return d
		}
		return v
	},
	// toJSON marshals any value to a JSON string. Safe for embedding in
	// body: fields or building dynamic JSON payloads. Aliased as `toJson`
	// and `tojson` so authors don't get bitten by Go template's
	// case-sensitive name lookup when copying from other ecosystems.
	"toJSON":   toJSON,
	"toJson":   toJSON,
	"tojson":   toJSON,
	"fromJSON": fromJSON,
	"fromJson": fromJSON,
	"fromjson": fromJSON,
	// jsonEscape escapes a string for safe embedding inside a JSON string
	// literal — replaces backslash, quote, and control characters.
	"jsonEscape": func(s string) string {
		b, _ := json.Marshal(s)
		// json.Marshal wraps in quotes — strip them
		return string(b[1 : len(b)-1])
	},
	// now returns the current UTC time as a formatted string.
	// Format uses Go reference time: "2006-01-02T15:04:05Z07:00"
	"now": func(format string) string {
		if format == "" {
			format = time.RFC3339
		}
		return time.Now().UTC().Format(format)
	},
}
