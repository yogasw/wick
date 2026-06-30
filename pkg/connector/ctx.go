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
	// masker is the encrypted-fields adapter the framework injects so
	// connectors can mask sensitive values that aren't declared as
	// `secret` Configs/Input fields (dynamic API responses, DB row data,
	// etc.) without importing internal/enc directly. nil when wick boots
	// without an enc service or with WICK_ENC_DISABLE — c.Mask /
	// c.MaskIgnoreCase then become passthroughs.
	masker Masker
	// rawInput holds the caller's arguments with their original JSON types
	// (bool, number, string, …) preserved, keyed exactly as `input`. It is
	// optional: only the MCP tools/call path populates it (via SetRawInput),
	// and only the MCP-proxy connector path reads it (via RawInputValue) to
	// forward a scalar to an upstream MCP server in its original type rather
	// than the stringified form. nil on every other path — readers MUST fall
	// back to the string `input` map when a key is absent.
	rawInput map[string]any
	// ownerBotID is the bot user id of the channel instance that owns this
	// call's session, pre-resolved by the framework (the connectors Service
	// is wired with a resolver that looks the session up in the channel
	// registry). Empty when the session isn't channel-backed, unknown, or no
	// resolver is wired. The Slack connector footer prefers this over its own
	// token-derived bot so a send names the session owner's bot, not the
	// sending connector's. Read via OwnerBotID().
	ownerBotID string
}

// Masker is the narrow slice of the encrypted-fields service
// connectors use to mask dynamic sensitive values they pull from
// upstream APIs. The framework provides an implementation pre-bound
// to the calling user's per-user key; connectors never see the user
// UUID directly.
//
// caseInsensitive selects between exact-match (false, default) and
// case-folded matching (true) — useful when a connector's keyword
// list should match "Admin" and "admin" alike. The token returned is
// derived from the keyword's configured form, so decrypt yields the
// configured spelling regardless of which case variant was masked.
type Masker interface {
	Mask(data string, values []string, caseInsensitive bool) string
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
// no-op when the reporter is absent. Pass a nil Masker when running
// without the encrypted-fields layer; c.Mask / c.MaskIgnoreCase become
// passthroughs.
func NewCtx(ctx context.Context, instanceID string, configs, input map[string]string, httpClient *http.Client, progress ProgressReporter, masker Masker) *Ctx {
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
		masker:     masker,
	}
}

// NewPluginCtx builds a Ctx inside a connector plugin subprocess from the
// plaintext creds + input the host sent over gRPC. There is no masker or
// progress reporter here — masking stays host-side; progress is bridged by
// the streaming server when used.
func NewPluginCtx(ctx context.Context, configs, input map[string]string) *Ctx {
	return &Ctx{ctx: ctx, HTTP: http.DefaultClient, configs: configs, input: input}
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

// Mask replaces every occurrence of each value in `values` inside
// `data` with a wick_enc_ token, scoped to the calling user's per-user
// key. Use it for sensitive plaintext that arrives from an upstream
// API and is NOT declared as a `secret` Configs/Input field — those
// are masked automatically by the framework.
//
// Identical values within one call receive identical tokens (per-call
// dedup cache), so the LLM does not mistake duplicates for distinct
// credentials. Returns `data` unchanged when the framework was booted
// without the encrypted-fields layer or with WICK_ENC_DISABLE set.
//
// Match is case-sensitive. For case-folded matching ("Admin" == "admin"
// share one token) use MaskIgnoreCase.
func (c *Ctx) Mask(data string, values []string) string {
	if c.masker == nil {
		return data
	}
	return c.masker.Mask(data, values, false)
}

// MaskIgnoreCase is the case-folded variant of Mask. Every case variant
// of a keyword in `data` is replaced with a single token derived from
// the keyword's configured form, so the round-tripped plaintext matches
// what was configured regardless of which case variant appeared in the
// upstream response.
func (c *Ctx) MaskIgnoreCase(data string, values []string) string {
	if c.masker == nil {
		return data
	}
	return c.masker.Mask(data, values, true)
}

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

// ── Full-map accessors ───────────────────────────────────────────────

// Configs returns a copy of the resolved per-instance credential map. The
// connector plugin adapter uses it to ship the full creds set (including
// host-injected OAuth keys like access_token) over gRPC. Returns a copy so
// callers cannot mutate the Ctx's internal state.
func (c *Ctx) Configs() map[string]string {
	out := make(map[string]string, len(c.configs))
	for k, v := range c.configs {
		out[k] = v
	}
	return out
}

// Inputs returns a copy of the per-call input map (same rationale as Configs).
func (c *Ctx) Inputs() map[string]string {
	out := make(map[string]string, len(c.input))
	for k, v := range c.input {
		out[k] = v
	}
	return out
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

// SetRawInput records the caller's arguments with their original JSON
// types preserved (see the rawInput field). The framework calls this
// once, right after constructing the Ctx, only on paths that have the
// untyped argument map at hand (MCP tools/call). It is a no-op-friendly
// setter: passing nil leaves rawInput nil and every RawInputValue read
// reports "absent", so connectors transparently keep using the string
// input map.
func (c *Ctx) SetRawInput(raw map[string]any) { c.rawInput = raw }

// RawInputValue returns the caller's original, untyped value for key —
// the value as it arrived in the tools/call request, before wick flattens
// arguments to strings. ok is false when no raw input was recorded for
// this call or the key is absent, in which case callers MUST fall back to
// the typed string accessors (Input / InputInt / InputBool). Connectors
// that proxy to an upstream MCP server use this to forward a bool or
// number in its original JSON type instead of a stringified form.
func (c *Ctx) RawInputValue(key string) (any, bool) {
	if c.rawInput == nil {
		return nil, false
	}
	v, ok := c.rawInput[key]
	return v, ok
}

// ── Session context ──────────────────────────────────────────────────

// SetOwnerBotID records the bot user id of the channel instance that owns
// this call's session, pre-resolved by the framework. The framework calls
// this once right after building the Ctx when it could resolve a session
// owner; other paths leave it empty.
func (c *Ctx) SetOwnerBotID(botUserID string) { c.ownerBotID = botUserID }

// OwnerBotID returns the bot user id of the channel instance that owns this
// call's session, pre-resolved by the framework. Empty when the session is
// not channel-backed, unknown, or no resolver is wired — callers must fall
// back to their own identity resolution.
func (c *Ctx) OwnerBotID() string { return c.ownerBotID }
