package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// supportedProtocolVersions lists every MCP revision this server can
// speak, newest first. The JSON-RPC method shapes (initialize,
// tools/list, tools/call) are identical across these revisions — the
// difference is transport (Streamable HTTP in 2025-03-26+ vs the
// 2024-11-05 single-shot POST). Wick is POST-only either way, which
// the spec allows for both: a Streamable-HTTP client that doesn't
// receive `text/event-stream` simply treats the response as a single
// JSON-RPC reply.
//
// latestProtocolVersion is the version returned when the client asks
// for one we don't support, per spec §lifecycle.
var supportedProtocolVersions = []string{"2025-03-26", "2024-11-05"}

const latestProtocolVersion = "2025-03-26"

// Handler wires the MCP JSON-RPC surface — tools/list, tools/call,
// initialize. Bearer auth is applied by AuthMiddleware before this
// handler runs, so every request reaches us with login.GetUser
// already populated.
//
// Tool surface uses a meta-tool pattern: rather than expose every
// (connector × operation) pair as its own static MCP tool, the server
// exposes three stable tools — wick_list, wick_search, wick_execute —
// and lets the LLM discover the underlying connectors and operations
// at runtime. Adding/removing connectors in the admin UI never changes
// what the client cached, so Claude.ai users don't have to click
// "Refresh tool list" after every admin change.
type Handler struct {
	connectors *connectors.Service
	version    string
	commit     string
	buildTime  string
}

func NewHandler(c *connectors.Service) *Handler {
	return &Handler{connectors: c, version: "dev", commit: "", buildTime: "unknown"}
}

// WithBuildInfo sets the version, short commit hash, and build timestamp
// shown in the MCP initialize response and wick_info tool.
// Called by RunMCPStdio with app.Build* vars.
func (h *Handler) WithBuildInfo(version, commit, buildTime string) *Handler {
	h.version = version
	if len(commit) > 8 {
		commit = commit[:8]
	}
	h.commit = commit
	h.buildTime = buildTime
	return h
}

func (h *Handler) serverVersion() string {
	if h.commit != "" && h.commit != "dev" {
		return h.version + " (" + h.commit + ")"
	}
	return h.version
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeRPCError(w, nil, errParseError, "could not read request", nil)
		return
	}
	defer r.Body.Close()

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, nil, errParseError, "invalid JSON", nil)
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, errInvalidRequest, "jsonrpc must be 2.0", nil)
		return
	}

	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Streamable HTTP (spec 2025-03-26): when the client lists
	// text/event-stream in Accept and the method actually benefits from
	// streaming (currently just tools/call — that's the path that calls
	// Execute and may emit progress notifications), respond with an SSE
	// body instead of a single JSON document. Other methods always
	// reply JSON regardless of Accept; the spec lets us choose.
	if req.Method == "tools/call" && wantsSSE(r) {
		h.handleToolsCallSSE(w, r, req)
		return
	}

	switch req.Method {
	case "initialize":
		h.handleInitialize(w, r, req)
	case "tools/list":
		h.handleToolsList(w, r, req)
	case "tools/call":
		h.handleToolsCall(w, r, req)
	case "ping":
		writeRPCResult(w, req.ID, struct{}{})
	default:
		writeRPCError(w, req.ID, errMethodNotFound, "unknown method: "+req.Method, nil)
	}
}

// ── initialize ───────────────────────────────────────────────────────

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	ServerInfo      serverInfo         `json:"serverInfo"`
	Capabilities    serverCapabilities `json:"capabilities"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type serverCapabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	// ListChanged would let us push tools/list_changed notifications.
	// We don't — and we don't need to. The meta-tool pattern keeps the
	// static tool list invariant (always exactly wick_list / wick_search
	// / wick_execute), so the client's cached list never goes stale.
	ListChanged bool `json:"listChanged"`
}

// initializeParams is the subset of the client's "initialize" payload
// we read. The full payload also carries clientInfo and capabilities,
// but wick doesn't react to those — capability negotiation here is
// one-way (server advertises its own).
type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

func (h *Handler) handleInitialize(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	var p initializeParams
	_ = json.Unmarshal(req.Params, &p) // tolerate older clients that omit params
	writeRPCResult(w, req.ID, initializeResult{
		ProtocolVersion: negotiateProtocolVersion(p.ProtocolVersion),
		ServerInfo:      serverInfo{Name: "wick", Version: h.serverVersion()},
		Capabilities:    serverCapabilities{Tools: toolsCapability{ListChanged: false}},
	})
}

// negotiateProtocolVersion implements the spec rule: if the client's
// requested version is one we support, echo it back; otherwise return
// our latest. Echoing the client's version keeps both sides on the
// same shape — important for clients that key transport behavior off
// the negotiated version (e.g. only opening a GET SSE stream after
// confirming 2025-03-26+).
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if v == requested {
			return v
		}
	}
	return latestProtocolVersion
}

// ── tools/list ───────────────────────────────────────────────────────

type toolListResult struct {
	Tools []toolDescriptor `json:"tools"`
}

// toolDescriptor is one entry in the static MCP tool list. Under the
// meta-tool pattern the list is always exactly the three entries in
// metaToolDescriptors — connector instances and per-op definitions are
// surfaced inside wick_list / wick_search payloads, not here.
type toolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema any             `json:"inputSchema"`
	Annotations *toolAnnotation `json:"annotations,omitempty"`
}

// toolAnnotation conveys MCP tool hints to the client. Claude.ai groups
// tools by readOnlyHint / destructiveHint and applies different default
// permission policies per group ("read-only" → ask, "write/delete" →
// prompt-and-allow). Without these every tool defaults to destructive.
type toolAnnotation struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

func ptrBool(b bool) *bool { return &b }

func (h *Handler) metaToolDescriptors() []toolDescriptor {
	return []toolDescriptor{
		{
			Name: "wick_list",
			Description: "List available connectors grouped by instance. " +
				"Returns each connector's id, label, description, and total_tools count — no schemas. " +
				"WORKFLOW: (1) wick_list to see what connectors exist, " +
				"(2) wick_get with the connector id to see its tools + input_schemas, " +
				"(3) wick_execute with tool_id + params. Takes no arguments.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &toolAnnotation{
				Title:        "List wick connectors",
				ReadOnlyHint: ptrBool(true),
			},
		},
		{
			Name: "wick_search",
			Description: "Search tools by keyword across all connectors. " +
				"Case-insensitive match on connector label, tool name, and description. " +
				"Returns matching tools nested under their connector (id, description), with tool_id per hit. " +
				"WORKFLOW: after finding a match, call wick_get with the connector id to get full schemas, " +
				"then wick_execute.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Keyword to match.",
					},
				},
				"required": []string{"query"},
			},
			Annotations: &toolAnnotation{
				Title:        "Search wick tools",
				ReadOnlyHint: ptrBool(true),
			},
		},
		{
			Name: "wick_get",
			Description: "Get a connector's full tool list with input_schemas. " +
				"Pass the connector id from wick_list or wick_search. " +
				"ALWAYS call this before wick_execute to know the required params. " +
				"Never guess params — read input_schema from this response first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Connector id from wick_list or wick_search.",
					},
				},
				"required": []string{"id"},
			},
			Annotations: &toolAnnotation{
				Title:        "Get wick connector tools",
				ReadOnlyHint: ptrBool(true),
			},
		},
		{
			Name: "wick_execute",
			Description: "Execute a tool by tool_id. " +
				"PREREQUISITE: call wick_get first to get the tool's input_schema — " +
				"never guess params. params must match the input_schema exactly. " +
				"On success returns the response as JSON; " +
				"on failure returns {\"error\": string, \"tool_id\": string} with isError=true.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_id": map[string]any{
						"type":        "string",
						"description": "Opaque tool identifier from wick_list or wick_search.",
					},
					"params": map[string]any{
						"type":                 "object",
						"description":          "Arguments matching the tool's input_schema. Use {} when the tool has no input fields.",
						"additionalProperties": true,
					},
				},
				"required": []string{"tool_id", "params"},
			},
			Annotations: &toolAnnotation{
				Title:           "Execute wick tool",
				ReadOnlyHint:    ptrBool(false),
				DestructiveHint: ptrBool(true),
				OpenWorldHint:   ptrBool(true),
			},
		},
		{
			Name:        "wick_info",
			Description: "Return wick server version and build info. Use this when asked about the version, build, or commit of the running wick instance.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &toolAnnotation{
				Title:        "Wick server info",
				ReadOnlyHint: ptrBool(true),
			},
		},
	}
}

func (h *Handler) handleToolsList(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	writeRPCResult(w, req.ID, toolListResult{Tools: h.metaToolDescriptors()})
}

// ── tools/call ───────────────────────────────────────────────────────

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	// Meta carries the optional _meta object the spec defines for tools/
	// call. Wick reads only progressToken from it — everything else is
	// transparent. Token type is RawMessage because the spec allows
	// either string or number; we echo it verbatim in progress notifs
	// so the client matches them to its outstanding request.
	Meta *callMeta `json:"_meta,omitempty"`
}

type callMeta struct {
	ProgressToken json.RawMessage `json:"progressToken,omitempty"`
}

// toolCallResult is the MCP-spec content envelope. We always return a
// single text part with a JSON-encoded payload — the spec allows
// multipart but JSON-in-text is what LLMs handle best.
type toolCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (h *Handler) handleToolsCall(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	user := login.GetUser(r.Context())
	if user == nil {
		writeRPCError(w, req.ID, errInternal, "no authenticated user on context", nil)
		return
	}
	tagIDs := login.GetUserTagIDs(r.Context())

	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, errInvalidParams, "invalid params: "+err.Error(), nil)
		return
	}

	switch p.Name {
	case "wick_list":
		h.handleWickList(w, r, req, tagIDs, user.IsAdmin())
	case "wick_search":
		h.handleWickSearch(w, r, req, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_get":
		h.handleWickGet(w, r, req, p.Arguments, tagIDs, user.IsAdmin())
	case "wick_execute":
		h.handleWickExecute(w, r, req, p.Arguments, user, tagIDs)
	case "wick_info":
		h.handleWickInfo(w, req)
	default:
		writeRPCError(w, req.ID, errInvalidParams, "unknown tool: "+p.Name, nil)
	}
}

// ── wick_info ──────────────────────────────────────────────────────

func (h *Handler) handleWickInfo(w http.ResponseWriter, req rpcRequest) {
	info := map[string]string{
		"version":    h.version,
		"commit":     h.commit,
		"build_time": h.buildTime,
	}
	b, _ := json.Marshal(info)
	writeRPCResult(w, req.ID, toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(b)}},
	})
}

// ── wick_list ───────────────────────────────────────────────────────

// connectorSummary is one entry in the wick_list response.
// Grouped per connector instance — no tool schemas, just counts.
type connectorSummary struct {
	ID          string `json:"id"`
	Connector   string `json:"connector"`
	Description string `json:"description"`
	TotalTools  int    `json:"total_tools"`
}

type listResult struct {
	Connectors      []connectorSummary `json:"connectors"`
	TotalConnectors int                `json:"total_connectors"`
	TotalTools      int                `json:"total_tools"`
}

func (h *Handler) handleWickList(w http.ResponseWriter, r *http.Request, req rpcRequest, tagIDs []string, isAdmin bool) {
	rows, err := h.connectors.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		writeToolError(w, req.ID, "list connectors: "+err.Error(), "")
		return
	}
	summaries := make([]connectorSummary, 0, len(rows))
	totalTools := 0
	for _, row := range rows {
		mod, ok := h.connectors.Module(row.Key)
		if !ok {
			continue
		}
		states, err := h.connectors.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		count := 0
		for _, op := range mod.Operations {
			if states[op.Key] {
				count++
			}
		}
		if count == 0 {
			continue
		}
		totalTools += count
		summaries = append(summaries, connectorSummary{
			ID:          row.ID,
			Connector:   row.Label,
			Description: mod.Meta.Description,
			TotalTools:  count,
		})
	}
	writeToolJSON(w, req.ID, listResult{
		Connectors:      summaries,
		TotalConnectors: len(summaries),
		TotalTools:      totalTools,
	})
}

// ── wick_search ─────────────────────────────────────────────────────

// searchTool is one matching tool inside a searchGroup. Connector-level
// info (id, label, description) lives on the parent group so it isn't
// repeated for each hit on the same connector.
type searchTool struct {
	ToolID      string `json:"tool_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Destructive bool   `json:"destructive"`
}

// searchGroup nests matching tools under their owning connector.
type searchGroup struct {
	ID          string       `json:"id"`
	Connector   string       `json:"connector"`
	Description string       `json:"description"`
	Tools       []searchTool `json:"tools"`
}

type searchResult struct {
	Connectors []searchGroup `json:"connectors"`
	Total      int           `json:"total"`
	Query      string        `json:"query"`
}

func (h *Handler) handleWickSearch(w http.ResponseWriter, r *http.Request, req rpcRequest, args map[string]any, tagIDs []string, isAdmin bool) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		writeToolError(w, req.ID, "query is required", "")
		return
	}
	rows, err := h.connectors.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		writeToolError(w, req.ID, "search: "+err.Error(), "")
		return
	}
	needle := strings.ToLower(query)
	groups := make([]searchGroup, 0)
	total := 0
	for _, row := range rows {
		mod, ok := h.connectors.Module(row.Key)
		if !ok {
			continue
		}
		states, err := h.connectors.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		matched := make([]searchTool, 0)
		for _, op := range mod.Operations {
			if !states[op.Key] {
				continue
			}
			hay := strings.ToLower(row.Label + " " + op.Name + " " + op.Description)
			if !strings.Contains(hay, needle) {
				continue
			}
			matched = append(matched, searchTool{
				ToolID:      formatToolID(row.ID, op.Key),
				Name:        op.Name,
				Description: op.Description,
				Destructive: op.Destructive,
			})
		}
		if len(matched) == 0 {
			continue
		}
		total += len(matched)
		groups = append(groups, searchGroup{
			ID:          row.ID,
			Connector:   row.Label,
			Description: mod.Meta.Description,
			Tools:       matched,
		})
	}
	writeToolJSON(w, req.ID, searchResult{Connectors: groups, Total: total, Query: query})
}

// ── wick_get ────────────────────────────────────────────────────────

// toolDetail is one tool entry inside a connectorDetail response.
type toolDetail struct {
	ToolID      string     `json:"tool_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Destructive bool       `json:"destructive"`
	InputSchema jsonSchema `json:"input_schema"`
}

// connectorDetail is the full response for wick_get — one connector
// instance with all its enabled tools and their input_schemas.
type connectorDetail struct {
	ID          string       `json:"id"`
	Connector   string       `json:"connector"`
	Description string       `json:"description"`
	Tools       []toolDetail `json:"tools"`
}

func (h *Handler) handleWickGet(w http.ResponseWriter, r *http.Request, req rpcRequest, args map[string]any, tagIDs []string, isAdmin bool) {
	connectorID, _ := args["id"].(string)
	connectorID = strings.TrimSpace(connectorID)
	if connectorID == "" {
		writeToolError(w, req.ID, "id is required", "")
		return
	}
	allowed, err := h.connectors.IsVisibleTo(r.Context(), connectorID, tagIDs, isAdmin)
	if err != nil || !allowed {
		writeToolError(w, req.ID, "connector not found or not accessible", connectorID)
		return
	}
	row, err := h.connectors.Get(r.Context(), connectorID)
	if err != nil {
		writeToolError(w, req.ID, "get connector: "+err.Error(), connectorID)
		return
	}
	mod, ok := h.connectors.Module(row.Key)
	if !ok {
		writeToolError(w, req.ID, "connector module not registered", connectorID)
		return
	}
	states, err := h.connectors.OperationStates(r.Context(), row.ID, row.Key)
	if err != nil {
		writeToolError(w, req.ID, "load operation states: "+err.Error(), connectorID)
		return
	}
	tools := make([]toolDetail, 0, len(mod.Operations))
	for _, op := range mod.Operations {
		if !states[op.Key] {
			continue
		}
		tools = append(tools, toolDetail{
			ToolID:      formatToolID(row.ID, op.Key),
			Name:        op.Name,
			Description: op.Description,
			Destructive: op.Destructive,
			InputSchema: configsToJSONSchema(op.Input),
		})
	}
	writeToolJSON(w, req.ID, connectorDetail{
		ID:          row.ID,
		Connector:   row.Label,
		Description: mod.Meta.Description,
		Tools:       tools,
	})
}

// ── wick_execute ────────────────────────────────────────────────────

func (h *Handler) handleWickExecute(w http.ResponseWriter, r *http.Request, req rpcRequest, args map[string]any, user *entity.User, tagIDs []string) {
	toolID, _ := args["tool_id"].(string)
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		writeToolError(w, req.ID, "tool_id is required", toolID)
		return
	}

	connectorID, opKey, err := parseToolID(toolID)
	if err != nil {
		writeToolError(w, req.ID, err.Error(), toolID)
		return
	}

	// Re-check authorization at call time — the cached list the client
	// holds may be stale, and tags can change between list and call.
	allowed, err := h.connectors.IsVisibleTo(r.Context(), connectorID, tagIDs, user.IsAdmin())
	if err != nil || !allowed {
		writeToolError(w, req.ID, "tool_id not found or not accessible", toolID)
		return
	}

	rawParams, _ := args["params"].(map[string]any)
	input := stringifyArgs(rawParams)

	res, execErr := h.connectors.Execute(r.Context(), connectors.ExecuteParams{
		ConnectorID:  connectorID,
		OperationKey: opKey,
		Input:        input,
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       user.ID,
		IPAddress:    clientIP(r),
		UserAgent:    r.Header.Get("User-Agent"),
	})
	if execErr != nil {
		// Per MCP spec, tool errors return as a result envelope with
		// IsError=true so the LLM sees the message and can recover.
		body := execErr.Error()
		if res != nil && res.ResponseJSON != "" {
			body = res.ResponseJSON
		}
		writeToolError(w, req.ID, body, toolID)
		return
	}
	writeRPCResult(w, req.ID, toolCallResult{
		Content: []toolContent{{Type: "text", Text: res.ResponseJSON}},
		IsError: false,
	})
}

// ── tool_id helpers ─────────────────────────────────────────────────

// formatToolID produces the opaque identifier wick_list and wick_search
// hand to the LLM. Format: "conn:{connector_id}/{op_key}". The conn:
// prefix leaves room for future tool sources (e.g. "mcp:" for proxied
// remote MCP tools, "prompt:" for prompt-shaped tools) without
// reshuffling the parser.
func formatToolID(connectorID, opKey string) string {
	return "conn:" + connectorID + "/" + opKey
}

// parseToolID inverts formatToolID. Returns a friendly error when the
// shape is wrong so the LLM can correct itself on the next call.
func parseToolID(id string) (connectorID, opKey string, err error) {
	const prefix = "conn:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", errors.New("tool_id must start with 'conn:'")
	}
	connectorID, opKey, ok := strings.Cut(id[len(prefix):], "/")
	if !ok || connectorID == "" || opKey == "" {
		return "", "", errors.New("tool_id must be of the form 'conn:{connector_id}/{op_key}'")
	}
	return connectorID, opKey, nil
}

// ── response helpers ────────────────────────────────────────────────

// writeToolJSON wraps a typed payload as the text content of a
// successful tools/call result. The MCP spec requires content[].text
// to be a string, so we JSON-encode the payload here.
func writeToolJSON(w http.ResponseWriter, id json.RawMessage, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		writeRPCError(w, id, errInternal, "marshal: "+err.Error(), nil)
		return
	}
	writeRPCResult(w, id, toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(body)}},
		IsError: false,
	})
}

// writeToolError emits a tools/call result with isError=true and a
// JSON-shaped error body. Returning errors as result envelopes (rather
// than JSON-RPC errors) is what the spec asks for — the LLM should
// see the message and try to recover, not see a transport failure.
func writeToolError(w http.ResponseWriter, id json.RawMessage, message, toolID string) {
	body := map[string]string{"error": message}
	if toolID != "" {
		body["tool_id"] = toolID
	}
	bytes, _ := json.Marshal(body)
	writeRPCResult(w, id, toolCallResult{
		Content: []toolContent{{Type: "text", Text: string(bytes)}},
		IsError: true,
	})
}

// ── input shaping ───────────────────────────────────────────────────

// stringifyArgs flattens the LLM's JSON params (string|number|bool|null
// |object|array) into the string-keyed map connectors.Service.Execute
// expects — every Cfg/Input field is read via Ctx.Input(key) which
// returns string.
func stringifyArgs(args map[string]any) map[string]string {
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = stringifyOne(v)
	}
	return out
}

func stringifyOne(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		// JSON numbers decode to float64. Render integers without ".0"
		// so existing connectors that strconv.Atoi don't choke.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	default:
		// Fall back to JSON for objects/arrays — connectors that take
		// structured input declare it as a JSON-string field anyway.
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// clientIP returns the request's resolved client IP. Trusts the
// X-Forwarded-For first hop when present (the realIP middleware
// upstream already normalized it onto RemoteAddr, but defensive).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
