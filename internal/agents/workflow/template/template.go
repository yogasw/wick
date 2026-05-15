// Package template renders Go text/template strings against a
// workflow.RenderCtx. Strict missing-key handling so typos surface as
// errors instead of `<no value>`. Used by every executor that needs to
// interpolate {{.Event.X}} / {{.Node.X.Y}} / {{.Env.X}} / {{.Secret.X}}.
package template

import (
	"bytes"
	"fmt"
	"strings"
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
}
