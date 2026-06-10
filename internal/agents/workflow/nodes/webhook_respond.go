package nodes

import (
	"context"
	"net/http"
	"strconv"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// WebhookRespondExecutor emits a custom HTTP response back to the
// webhook caller. It only takes effect when the firing trigger has
// respond_mode = "respond_node"; for other modes the node runs as a
// no-op pass-through so the workflow still validates cleanly.
type WebhookRespondExecutor struct{}

// NewWebhookRespondExecutor wires the executor.
func NewWebhookRespondExecutor() *WebhookRespondExecutor { return &WebhookRespondExecutor{} }

// WebhookRespondSchema is the per-field schema reflected by
// integration.StructSchema and entity.StructToConfigs.
type WebhookRespondSchema struct {
	Status  string `wick:"key=respond_status;number;desc=HTTP status code to return (default 200)."`
	Body    string `wick:"key=respond_body;textarea;desc=Response body. Rendered as Go template — use {{.Node.x.field}} to embed upstream output."`
	Headers string `wick:"key=respond_headers;kvlist=name|value;desc=Extra response headers. Each value is template-rendered."`
}

// Execute captures the desired response fields into the node output.
// The webhook handler reads status/body/headers from RunState.Outputs
// after the run completes.
func (e *WebhookRespondExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	rctx := rc.RenderCtx()

	status := n.RespondStatus
	if status == 0 {
		status = http.StatusOK
	}

	body := n.RespondBody
	if body != "" {
		rendered, err := template.Render(body, rctx)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		body = rendered
	}

	headers := map[string]string{}
	for k, v := range n.RespondHeaders {
		rendered, err := template.Render(v, rctx)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		headers[k] = rendered
	}

	// _webhook_respond sentinel lets the handler distinguish this node's
	// output from http nodes (which also have status+body+headers).
	return workflow.NodeOutput{Fields: map[string]any{
		"_webhook_respond": true,
		"status":           status,
		"body":             body,
		"headers":          headers,
	}}, nil
}

// Descriptor exposes the schema + docs for the MCP catalog.
func (e *WebhookRespondExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Category:    engine.CategoryAction,
		Label:       "Respond to Webhook",
		Badge:       "HTTP response",
		Description: "Send a custom HTTP response to the webhook caller. Requires the trigger's respond_mode = \"respond_node\".",
		WhenToUse:   "When you need to control the HTTP status code, body, or headers returned to the webhook caller. For fire-and-forget webhooks use respond_mode = \"immediately\" on the trigger instead.",
		Example:     "{\n  \"id\": \"respond_ok\",\n  \"type\": \"webhook_respond\",\n  \"respond_status\": 200,\n  \"respond_body\": \"{\\\"order_id\\\": \\\"{{.Node.create_order.id}}\\\"}\",\n  \"respond_headers\": {\"Content-Type\": \"application/json\"}\n}",
		Schema:      integration.StructSchema(WebhookRespondSchema{}),
		Output: map[string]string{
			"status":  "int — HTTP status code that will be sent",
			"body":    "string — rendered response body",
			"headers": "map[string]string — rendered response headers",
		},
		Docs: wickdocs.Docs{
			Quirks: []string{
				"Only active when the firing webhook trigger has respond_mode = \"respond_node\". For all other respond modes this node executes as a no-op pass-through so the workflow still validates cleanly.",
				"The FIRST webhook_respond node that completes in the run wins. Subsequent ones in the same run are ignored by the handler.",
				"Default respond_status is 200 when the field is omitted or zero.",
				"respond_body and respond_headers values are Go templates — use {{.Node.<id>.<field>}} to embed upstream output.",
				"Timeout: the webhook handler waits at most 30 seconds for this node to complete. If the workflow takes longer, the caller receives HTTP 504 and the workflow continues running in the background.",
				"The 30-second window covers the entire workflow run, not just this node. Place it late in the graph only when all upstream nodes finish quickly.",
			},
			TemplateableFields: []string{"respond_body", "respond_headers.*"},
			PairWith:           []string{"http", "transform", "branch"},
			CommonPitfalls: []string{
				"Forgetting Content-Type in respond_headers when returning JSON — callers may not parse the body correctly.",
				"Using respond_node mode without a webhook_respond node in the graph — the handler always times out and returns 504.",
				"Placing this node after a slow operation (agent, long HTTP call) — the 30s clock starts when the webhook fires, not when this node starts.",
				"Returning a 2xx status with an empty body when the caller expects JSON — set both respond_body and Content-Type header.",
			},
			InputSample:  `{"respond_status": 200, "respond_body": "{\"id\": \"{{.Node.create.id}}\"}", "respond_headers": {"Content-Type": "application/json"}}`,
			OutputSample: `{"status": 200, "body": "{\"id\": \"abc123\"}", "headers": {"Content-Type": "application/json"}}`,
		},
	}
}

// respondStatusCode parses the status field from a webhook_respond
// node output. Falls back to 200 on missing / invalid values.
func respondStatusCode(out map[string]any) int {
	v, ok := out["status"]
	if !ok {
		return http.StatusOK
	}
	switch s := v.(type) {
	case int:
		return s
	case float64:
		return int(s)
	case string:
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return http.StatusOK
}
