package custom

import (
	"encoding/base64"
	"fmt"
	"strings"
	"text/template"
)

// maxTemplateOutput caps a single rendered template (URL, header value,
// or body). Templates expand admin-authored recipes with LLM-supplied
// inputs, so an unbounded render could balloon memory on a hostile
// input; 1 MB is far beyond any legitimate request body wick should
// originate.
const maxTemplateOutput = 1 << 20

// templateFuncs is the whitelist available inside url_template, header
// values, and body_template. text/template has no file or process
// builtins, so the sandbox boundary is: these functions + the safe
// built-in pipeline set (printf, js, urlquery, ...). Nothing here may
// touch the filesystem, network, or environment.
var templateFuncs = template.FuncMap{
	// default returns fallback when the value is empty.
	"default": func(fallback, value string) string {
		if value == "" {
			return fallback
		}
		return value
	},
	"lower": strings.ToLower,
	"upper": strings.ToUpper,
	// b64 is needed for Basic-auth recipes:
	// `Basic {{b64 (printf "%s:%s" .cfg.basic_user .cfg.basic_pass)}}`.
	"b64": func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	},
}

// renderTemplate renders one stored template string against the cfg/in
// maps — .cfg.<key> holds resolved per-instance config values (already
// decrypted by the connectors framework), .in.<key> the per-call input. missingkey=error turns a typo'd `{{.cfg.api_keyy}}` into a
// clear connector_runs.error_msg instead of silently emitting
// "<no value>" at the upstream API.
func renderTemplate(name, tmpl string, cfg, in map[string]string) (string, error) {
	if cfg == nil {
		cfg = map[string]string{}
	}
	if in == nil {
		in = map[string]string{}
	}
	t, err := template.New(name).
		Funcs(templateFuncs).
		Option("missingkey=error").
		Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("%s template: %w", name, err)
	}
	var sb strings.Builder
	// text/template needs map[string]any-ish access via field names; a
	// lowercase-keyed map keeps {{.cfg.base_url}} working.
	data := map[string]any{"cfg": toAnyMap(cfg), "in": toAnyMap(in)}
	if err := t.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("%s template: %w", name, err)
	}
	if sb.Len() > maxTemplateOutput {
		return "", fmt.Errorf("%s template: rendered output exceeds %d bytes", name, maxTemplateOutput)
	}
	return sb.String(), nil
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
