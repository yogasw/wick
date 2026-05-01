package mcp

import (
	"context"
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

// protocolVersion is the MCP version this server speaks. Bumped when
// the JSON-RPC surface changes shape; clients negotiate via the
// "initialize" handshake.
const protocolVersion = "2024-11-05"

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
}

func NewHandler(c *connectors.Service) *Handler {
	return &Handler{connectors: c}
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

func (h *Handler) handleInitialize(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	writeRPCResult(w, req.ID, initializeResult{
		ProtocolVersion: protocolVersion,
		ServerInfo:      serverInfo{Name: "wick", Version: "0.4.0"},
		Capabilities:    serverCapabilities{Tools: toolsCapability{ListChanged: false}},
	})
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

// metaToolDescriptors returns the fixed three-tool surface every
// authenticated MCP client receives. Kept as a function (not a package
// var) so the input schemas are fresh maps per call — JSON encoding
// mutates fields in-place in some clients and we don't want shared
// state surprises.
func metaToolDescriptors() []toolDescriptor {
	return []toolDescriptor{
		{
			Name: "wick_list",
			Description: "List every connector operation the caller can execute. " +
				"Returns each entry's tool_id, connector label, operation name, description, " +
				"destructive flag, and input_schema. Call this to discover what's available, " +
				"then pass an entry's tool_id to wick_execute. Takes no arguments.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &toolAnnotation{
				Title:        "List wick tools",
				ReadOnlyHint: ptrBool(true),
			},
		},
		{
			Name: "wick_search",
			Description: "Search the caller's connector operations by keyword. " +
				"Case-insensitive substring match across connector label, operation name, " +
				"and operation description. Returns the same shape as wick_list. " +
				"Prefer this over wick_list when you have a specific intent " +
				"(e.g. \"create user\", \"list invoices\").",
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
			Name: "wick_execute",
			Description: "Execute one connector operation by tool_id. tool_id comes from " +
				"wick_list or wick_search; params is an object whose shape matches the " +
				"operation's input_schema (use {} for ops with no input). On success " +
				"returns the operation's response payload as JSON; on failure returns " +
				"{\"error\": string, \"tool_id\": string} with isError=true.",
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
	}
}

func (h *Handler) handleToolsList(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	writeRPCResult(w, req.ID, toolListResult{Tools: metaToolDescriptors()})
}

// ── tools/call ───────────────────────────────────────────────────────

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
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
	case "wick_execute":
		h.handleWickExecute(w, r, req, p.Arguments, user, tagIDs)
	default:
		writeRPCError(w, req.ID, errInvalidParams, "unknown tool: "+p.Name, nil)
	}
}

// ── wick_list / wick_search ─────────────────────────────────────────

// discoveredTool is one entry inside the wick_list / wick_search text
// payload. tool_id is the opaque handle wick_execute consumes; the
// rest is the human/LLM-facing context that lets the model pick the
// right tool without a separate describe call.
type discoveredTool struct {
	ToolID      string     `json:"tool_id"`
	Connector   string     `json:"connector"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Destructive bool       `json:"destructive"`
	InputSchema jsonSchema `json:"input_schema"`
}

type discoverResult struct {
	Tools []discoveredTool `json:"tools"`
	Total int              `json:"total"`
	Query string           `json:"query,omitempty"`
}

func (h *Handler) handleWickList(w http.ResponseWriter, r *http.Request, req rpcRequest, tagIDs []string, isAdmin bool) {
	tools, err := h.collectVisibleTools(r.Context(), tagIDs, isAdmin)
	if err != nil {
		writeToolError(w, req.ID, "list tools: "+err.Error(), "")
		return
	}
	writeToolJSON(w, req.ID, discoverResult{Tools: tools, Total: len(tools)})
}

func (h *Handler) handleWickSearch(w http.ResponseWriter, r *http.Request, req rpcRequest, args map[string]any, tagIDs []string, isAdmin bool) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		writeToolError(w, req.ID, "query is required", "")
		return
	}
	all, err := h.collectVisibleTools(r.Context(), tagIDs, isAdmin)
	if err != nil {
		writeToolError(w, req.ID, "search tools: "+err.Error(), "")
		return
	}
	needle := strings.ToLower(query)
	hits := make([]discoveredTool, 0, len(all))
	for _, t := range all {
		hay := strings.ToLower(t.Connector + " " + t.Name + " " + t.Description)
		if strings.Contains(hay, needle) {
			hits = append(hits, t)
		}
	}
	writeToolJSON(w, req.ID, discoverResult{Tools: hits, Total: len(hits), Query: query})
}

// collectVisibleTools enumerates every (connector × enabled operation)
// the caller can access, formatting one discoveredTool per pair.
// Shared by wick_list and wick_search so visibility/enable filtering
// stays consistent between the two.
func (h *Handler) collectVisibleTools(ctx context.Context, tagIDs []string, isAdmin bool) ([]discoveredTool, error) {
	rows, err := h.connectors.ListVisibleTo(ctx, tagIDs, isAdmin)
	if err != nil {
		return nil, err
	}
	out := make([]discoveredTool, 0, len(rows))
	for _, row := range rows {
		mod, ok := h.connectors.Module(row.Key)
		if !ok {
			continue
		}
		states, err := h.connectors.OperationStates(ctx, row.ID, row.Key)
		if err != nil {
			continue
		}
		for _, op := range mod.Operations {
			if !states[op.Key] {
				continue
			}
			out = append(out, discoveredTool{
				ToolID:      formatToolID(row.ID, op.Key),
				Connector:   row.Label,
				Name:        op.Name,
				Description: op.Description,
				Destructive: op.Destructive,
				InputSchema: configsToJSONSchema(op.Input),
			})
		}
	}
	return out, nil
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
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
