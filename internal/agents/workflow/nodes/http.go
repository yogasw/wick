package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// HTTPExecutor performs an HTTP request. Retry policy from n.Retry;
// default GET; parse_response raw|json|bytes.
type HTTPExecutor struct {
	client *http.Client
}

// NewHTTPExecutor builds the HTTP executor with a 30s default client.
func NewHTTPExecutor() *HTTPExecutor {
	return &HTTPExecutor{client: &http.Client{Timeout: 30 * time.Second}}
}

// HTTPSchema is the per-field schema reflected via integration.StructSchema
// for workflow_node_types. Single source of truth for AI consumers and
// the editor UI (the editor-side module reflects the same struct via
// entity.StructToConfigs to render ArgForm).
type HTTPSchema struct {
	Method        string `wick:"required;key=method;dropdown=GET|POST|PUT|PATCH|DELETE;desc=HTTP method"`
	URL           string `wick:"required;key=url;desc=Full URL. Rendered as Go template — use {{.Node.x.y}} or {{.Event.Payload.z}} to pull values from upstream nodes."`
	Headers       string `wick:"key=headers;kvlist=name|value;desc=Request headers. Each value is rendered as a Go template, so you can pull tokens from upstream nodes (e.g. Authorization: Bearer {{.Node.login.token}})."`
	Query         string `wick:"key=query;kvlist=name|value;desc=Query string params. Each value is rendered as a Go template."`
	Body          string `wick:"key=body;textarea;visible_when=method:POST|PUT|PATCH|DELETE;desc=Request body as string. Use YAML block scalar | for multiline JSON."`
	ParseResponse string `wick:"key=parse_response;dropdown=raw|json|bytes;visible_when=method:POST|PUT|PATCH|DELETE;desc=How to parse response body (default: raw)"`
	TimeoutSec    string `wick:"key=timeout_sec;number;desc=Request timeout in seconds (default 30)"`
}

// Dependencies surfaces the URL (or its host when easy to extract)
// as a generic http dependency so workflow_describe groups HTTP
// outbound under deps.other.http.
func (e *HTTPExecutor) Dependencies(n workflow.Node) []engine.NodeDependency {
	if n.URL == "" {
		return nil
	}
	return []engine.NodeDependency{{Kind: engine.DepKindHTTP, Ref: n.URL}}
}

// TemplateableFields exposes per-header / per-query values to
// workflow_describe's cross-ref scan in addition to the generic
// pool (url + body live in the default set).
func (e *HTTPExecutor) TemplateableFields(n workflow.Node) map[string]string {
	out := map[string]string{}
	for k, v := range n.Headers {
		out["headers."+k] = v
	}
	for k, v := range n.Query {
		out["query."+k] = v
	}
	return out
}

// Descriptor exposes the schema + docs for the MCP catalog.
func (e *HTTPExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Make an HTTP request. URL/headers/query/body rendered as Go templates.",
		WhenToUse:   "Direct external API calls without a connector module.",
		Example:     "- id: call_api\n  type: http\n  method: POST\n  url: https://api.example.com/tickets\n  headers:\n    Content-Type: application/json\n  body: |\n    {\"title\": \"{{jsonEscape (index .Event.Payload \\\"text\\\")}}\"}",
		Schema:      integration.StructSchema(HTTPSchema{}),
		Output: map[string]string{
			"status":  "int — HTTP status code",
			"body":    "string — response body",
			"headers": "map[string]string — response headers",
		},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"status":  "Numeric HTTP status. Branch on it via a downstream branch node ({{.Node.x.status}} >= 400).",
				"body":    "Response body as string. Always populated regardless of parse_response.",
				"headers": "Flat map of response headers — first value per key.",
				"json":    "Parsed JSON body. Populated when parse_response is \"json\" or unset and the body is valid JSON. Use {{.Node.x.json.<field>}}.",
				"bytes":   "Raw bytes — populated only when parse_response is \"bytes\".",
			},
			TemplateableFields: []string{"url", "body", "headers", "query"},
			Quirks: []string{
				"Default timeout is 30s. Override with timeout_sec when the upstream is known to be slow.",
				"Retry policy comes from the common retry block (max, backoff_sec). Retries fire on transport errors and 5xx — NOT on 4xx (those are user errors).",
				"headers / query are kv-list widgets in the inspector — each VALUE is template-rendered. Set arg_modes.headers: fixed (or query: fixed) to render the whole map literally.",
				"parse_response defaults to \"json\" — when the body is valid JSON, .Node.<this>.json is populated. Set to \"raw\" or \"bytes\" to skip parsing.",
				"jsonEscape your template values when embedding into a JSON body — unescaped quotes break the payload.",
			},
			PairWith: []string{"branch", "transform", "shell"},
			CommonPitfalls: []string{
				"Don't put secrets directly in headers — use wick_enc_ tokens, or reference them via {{.Env.MY_SECRET}}.",
				"Don't depend on .Node.<this>.json when parse_response is \"raw\" — only .body is populated.",
				"Don't forget Content-Type: application/json when posting JSON — wick doesn't auto-set it.",
			},
			InputSample:  `{"method":"POST","url":"https://api.example.com/tickets","headers":{"Authorization":"Bearer {{.Env.API_TOKEN}}","Content-Type":"application/json"},"body":"{\"title\":\"{{jsonEscape .Node.classify.reasoning}}\"}","parse_response":"json","timeout_sec":"15"}`,
			OutputSample: `{"status":201,"body":"{\"id\":42,\"title\":\"Payment refund bug\"}","headers":{"Content-Type":"application/json"},"json":{"id":42,"title":"Payment refund bug"}}`,
			Examples: []wickdocs.Example{
				{
					Name: "post_json_body",
					YAML: `- id: file_ticket
  type: http
  method: POST
  url: https://api.example.com/tickets
  headers:
    Content-Type: application/json
    Authorization: "Bearer {{.Env.API_TOKEN}}"
  body: |
    {"title": "{{jsonEscape .Node.classify.reasoning}}"}
  parse_response: json
  timeout_sec: "15"`,
				},
				{
					Name: "get_with_query",
					YAML: `- id: lookup
  type: http
  method: GET
  url: https://api.example.com/users
  query:
    email: "{{.Node.trigger.payload.email}}"`,
				},
			},
		},
	}
}

// renderHTTPField honors per-field ArgModes: when n.ArgModes[key] is
// "fixed" the value is returned verbatim (no template render), otherwise
// it falls through template.Render. Default = expression (backward compat
// with workflows authored before the inspector grew the toggle).
func renderHTTPField(n workflow.Node, key, raw string, rctx workflow.RenderCtx) (string, error) {
	if mode, ok := n.ArgModes[key]; ok && mode == "fixed" {
		return raw, nil
	}
	return template.Render(raw, rctx)
}

// Execute runs the request described by node n.
func (e *HTTPExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	rctx := rc.RenderCtx()
	method := strings.ToUpper(n.Method)
	if method == "" {
		method = http.MethodGet
	}
	urlStr, err := renderHTTPField(n, "url", n.URL, rctx)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("url: %w", err)
	}
	if len(n.Query) > 0 {
		u, err := url.Parse(urlStr)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("url parse: %w", err)
		}
		q := u.Query()
		// Per-field mode applies to the whole `query` map — fixed
		// means every value is taken verbatim, expression renders
		// each through template.Render. Mixed modes per inner key
		// are not exposed yet (the textarea is one widget).
		fixedQuery := n.ArgModes["query"] == "fixed"
		for k, v := range n.Query {
			rv := v
			if !fixedQuery {
				rendered, rerr := template.Render(v, rctx)
				if rerr != nil {
					return workflow.NodeOutput{}, fmt.Errorf("query %q: %w", k, rerr)
				}
				rv = rendered
			}
			q.Set(k, rv)
		}
		u.RawQuery = q.Encode()
		urlStr = u.String()
	}

	body := io.Reader(nil)
	if n.Body != "" {
		rb, err := renderHTTPField(n, "body", n.Body, rctx)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("body: %w", err)
		}
		body = strings.NewReader(rb)
	}

	timeout := time.Duration(n.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	maxAttempts := 1
	backoff := time.Second
	if n.Retry != nil {
		if n.Retry.Max > 0 {
			maxAttempts = n.Retry.Max + 1
		}
		if n.Retry.BackoffSec > 0 {
			backoff = time.Duration(n.Retry.BackoffSec) * time.Second
		}
	}

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(cctx, method, urlStr, body)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("http build: %w", err)
		}
		fixedHeaders := n.ArgModes["headers"] == "fixed"
		for k, v := range n.Headers {
			rv := v
			if !fixedHeaders {
				rendered, rerr := template.Render(v, rctx)
				if rerr != nil {
					return workflow.NodeOutput{}, fmt.Errorf("header %q: %w", k, rerr)
				}
				rv = rendered
			}
			req.Header.Set(k, rv)
		}
		resp, lastErr = e.client.Do(req)
		if lastErr == nil && resp.StatusCode < 500 {
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
			resp = nil
		}
		if attempt < maxAttempts-1 {
			select {
			case <-cctx.Done():
				return workflow.NodeOutput{}, cctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	if lastErr != nil {
		return workflow.NodeOutput{}, fmt.Errorf("http exec: %w", lastErr)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("http read body: %w", err)
	}
	out := workflow.NodeOutput{
		Fields: map[string]any{
			"status":  resp.StatusCode,
			"headers": flattenHeaders(resp.Header),
			"body":    string(raw),
		},
	}
	switch n.ParseResponse {
	case "json", "":
		var v any
		err := json.Unmarshal(raw, &v)
		if err == nil {
			out.Fields["json"] = v
		} else if n.ParseResponse == "json" {
			return workflow.NodeOutput{}, fmt.Errorf("parse_response json: %w", err)
		}
	case "raw":
	case "bytes":
		out.Fields["bytes"] = raw
	}
	return out, nil
}

func flattenHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
