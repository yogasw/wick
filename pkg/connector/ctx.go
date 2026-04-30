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
	ctx    context.Context
	HTTP   *http.Client
	configs map[string]string
	input   map[string]string
	// instanceID is the connector_instances.id this call is bound to.
	// Modules rarely need it, but it is exposed via InstanceID() for
	// logging and for MCP-side audit trails.
	instanceID string
}

// NewCtx is used by wick when dispatching an MCP tools/call or a panel
// test. Downstream code does not call this directly.
func NewCtx(ctx context.Context, instanceID string, configs, input map[string]string, httpClient *http.Client) *Ctx {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Ctx{
		ctx:        ctx,
		HTTP:       httpClient,
		configs:    configs,
		input:      input,
		instanceID: instanceID,
	}
}

// Context returns the context.Context bound to this call. Use it for
// every outbound HTTP request and downstream service call so cancellation
// from the MCP client propagates correctly.
func (c *Ctx) Context() context.Context { return c.ctx }

// InstanceID returns the connector_instances.id this call is bound to.
// Useful for structured logging.
func (c *Ctx) InstanceID() string { return c.instanceID }

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
