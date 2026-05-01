package connector

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

// Ctx is the per-call handle wick passes to a connector's ExecuteFunc.
// It bundles the resolved per-instance credential map, the per-call
// input arguments from the LLM, an HTTP client, and a context.Context
// so the call participates in cancellation and deadlines.
//
// Unlike tool.Ctx and job.Ctx, the values in Ctx are NOT looked up via
// a global config service keyed on Owner+Key. A connector instance has
// its own row of credential values, materialized into c.configs at
// dispatch time, so reads stay O(1) and don't touch the configs table.
//
// Construction is internal to wick — modules receive a ready-made *Ctx
// from the MCP dispatch layer (and, later, from the panel-test handler).
type Ctx struct {
	ctx context.Context
	// HTTP is the shared client every connector should use for outbound
	// calls. Connectors MUST build requests with
	//
	//	http.NewRequestWithContext(c.Context(), method, url, body)
	//
	// (NOT plain http.NewRequest) so the request inherits the call's
	// deadline. Without that, the MCP transport's per-call timeout
	// (sseExecuteTimeout in internal/mcp) and client-disconnect
	// cancellations cannot abort an in-flight upstream request — the
	// goroutine would leak until the upstream eventually responds on
	// its own, accumulating one such goroutine per stuck call.
	HTTP    *http.Client
	configs map[string]string
	input   map[string]string
	// instanceID is the connector_instances.id this call is bound to.
	// Modules rarely need it, but it is exposed via InstanceID() for
	// logging and for MCP-side audit trails.
	instanceID string
	// progress is the optional reporter the MCP transport injects when
	// the client opted into Streamable HTTP (Accept: text/event-stream).
	// Always read it via ReportProgress so connectors don't have to nil-
	// check on every call.
	progress ProgressReporter
}

// ProgressReporter receives incremental progress events emitted by a
// connector during a long-running call. The MCP layer wires an
// implementation that pushes JSON-RPC notifications/progress frames
// over the active SSE response; the JSON transport supplies no
// reporter and ReportProgress becomes a no-op.
//
// Report MUST NOT block the caller — implementations drop events when
// the client is slow or has disconnected rather than back-pressuring
// the connector that emits them.
type ProgressReporter interface {
	Report(progress, total int, message string)
}

// NewCtx is used by wick when dispatching an MCP tools/call or a panel
// test. Downstream code does not call this directly. Pass a nil
// ProgressReporter for non-streaming calls; ReportProgress will be a
// no-op when the reporter is absent.
func NewCtx(ctx context.Context, instanceID string, configs, input map[string]string, httpClient *http.Client, progress ProgressReporter) *Ctx {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Ctx{
		ctx:        ctx,
		HTTP:       httpClient,
		configs:    configs,
		input:      input,
		instanceID: instanceID,
		progress:   progress,
	}
}

// Context returns the context.Context bound to this call. The MCP
// transport derives this ctx from the inbound HTTP request and may
// further wrap it with a deadline (e.g. the SSE path's per-call
// timeout). Connectors MUST plumb this ctx into every outbound HTTP
// request and downstream service call so:
//
//   - the call aborts promptly when the deadline fires, instead of
//     hanging until the upstream API returns on its own
//   - the client-disconnect signal propagates and the goroutine
//     servicing the call winds down rather than leaking
//
// Skipping this is the single most common way to introduce a
// goroutine leak in a connector.
func (c *Ctx) Context() context.Context { return c.ctx }

// InstanceID returns the connector_instances.id this call is bound to.
// Useful for structured logging.
func (c *Ctx) InstanceID() string { return c.instanceID }

// ReportProgress emits a progress event to the active MCP session, if
// one is listening. Safe to call from any goroutine. When the call was
// initiated over the JSON transport (no streaming) this is a no-op,
// so connectors can call it freely without checking for a reporter.
//
// Pass total = 0 when the total is unknown — the MCP client renders
// the message and a spinner instead of a percentage. progress is the
// monotonically increasing units-completed count; values that go
// backwards confuse some clients.
func (c *Ctx) ReportProgress(progress, total int, message string) {
	if c.progress == nil {
		return
	}
	c.progress.Report(progress, total, message)
}

// ── Credential reads ─────────────────────────────────────────────────

// Cfg returns the value of a credential field declared by this
// connector's Creds struct. Returns "" when the key is absent.
func (c *Ctx) Cfg(key string) string {
	if c.configs == nil {
		return ""
	}
	return c.configs[key]
}

// CfgInt returns c.Cfg(key) parsed as int. Unparseable or empty values
// return 0.
func (c *Ctx) CfgInt(key string) int {
	n, _ := strconv.Atoi(c.Cfg(key))
	return n
}

// CfgBool returns c.Cfg(key) parsed as bool. "true"/"1"/"yes"/"on"
// (case-insensitive) count as true; anything else is false.
func (c *Ctx) CfgBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(c.Cfg(key))) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// ── Input reads ──────────────────────────────────────────────────────

// Input returns the value of an argument the LLM passed in this
// tools/call request. The set of valid keys is declared by the
// connector's Input struct. Returns "" when the key is absent.
func (c *Ctx) Input(key string) string {
	if c.input == nil {
		return ""
	}
	return c.input[key]
}

// InputInt returns c.Input(key) parsed as int. Unparseable or empty
// values return 0.
func (c *Ctx) InputInt(key string) int {
	n, _ := strconv.Atoi(c.Input(key))
	return n
}

// InputBool returns c.Input(key) parsed as bool. "true"/"1"/"yes"/"on"
// (case-insensitive) count as true; anything else is false.
func (c *Ctx) InputBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(c.Input(key))) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
