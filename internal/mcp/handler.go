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
	"github.com/yogasw/wick/pkg/connector"
)

// protocolVersion is the MCP version this server speaks. Bumped when
// the JSON-RPC surface changes shape; clients negotiate via the
// "initialize" handshake.
const protocolVersion = "2024-11-05"

// Handler wires the MCP JSON-RPC surface — tools/list, tools/call,
// initialize. Bearer auth is applied by AuthMiddleware before this
// handler runs, so every request reaches us with login.GetUser
// already populated.
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

	// Notifications: best-effort accept, no body.
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
	// ListChanged indicates the server can push tools/list_changed
	// notifications. We don't (yet) — clients should poll.
	ListChanged bool `json:"listChanged"`
}

func (h *Handler) handleInitialize(w http.ResponseWriter, _ *http.Request, req rpcRequest) {
	writeRPCResult(w, req.ID, initializeResult{
		ProtocolVersion: protocolVersion,
		ServerInfo:      serverInfo{Name: "wick", Version: "0.3.0"},
		Capabilities:    serverCapabilities{Tools: toolsCapability{ListChanged: false}},
	})
}

// ── tools/list ───────────────────────────────────────────────────────

type toolListResult struct {
	Tools []toolDescriptor `json:"tools"`
}

type toolDescriptor struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema jsonSchema `json:"inputSchema"`
}

func (h *Handler) handleToolsList(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	user := login.GetUser(r.Context())
	if user == nil {
		writeRPCError(w, req.ID, errInternal, "no authenticated user on context", nil)
		return
	}
	tagIDs := login.GetUserTagIDs(r.Context())

	rows, err := h.connectors.ListVisibleTo(r.Context(), tagIDs, user.IsAdmin())
	if err != nil {
		writeRPCError(w, req.ID, errInternal, "list connectors: "+err.Error(), nil)
		return
	}

	tools := make([]toolDescriptor, 0, len(rows))
	for _, row := range rows {
		mod, ok := h.connectors.Module(row.Key)
		if !ok {
			// Code definition was removed in a deploy that left this
			// row behind. Skip silently — admin sees it as deactivated
			// in the UI; LLM doesn't need to know.
			continue
		}
		states, err := h.connectors.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		for _, op := range mod.Operations {
			if !states[op.Key] {
				continue
			}
			tools = append(tools, toolDescriptor{
				Name:        toolName(row, op),
				Description: opDescription(mod.Meta, row, op),
				InputSchema: configsToJSONSchema(op.Input),
			})
		}
	}
	writeRPCResult(w, req.ID, toolListResult{Tools: tools})
}

// toolName produces the public MCP tool name for a (row, op) pair:
//
//	{connector_key}__{op_key}__{row_label_slug}
//
// Three segments make collisions vanishingly rare while staying
// human-readable. Underscore separator is the safest choice across
// every MCP client we've checked (some balk at dots).
func toolName(row entity.Connector, op connector.Operation) string {
	return row.Key + "__" + op.Key + "__" + slugify(row.Label)
}

// opDescription is what the LLM reads to decide whether to call.
// Combines the connector-level Meta description with the op-specific
// description; instance Label is appended in parens so duplicate ops
// across instances stay distinguishable.
func opDescription(meta connector.Meta, row entity.Connector, op connector.Operation) string {
	desc := op.Description
	if desc == "" {
		desc = op.Name
	}
	suffix := " (" + row.Label + ")"
	if meta.Description != "" {
		return meta.Description + " — " + desc + suffix
	}
	return desc + suffix
}

// ── tools/call ───────────────────────────────────────────────────────

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// toolCallResult is the MCP-spec content envelope. We always return a
// single text part with the JSON-encoded ExecuteFunc return value;
// the spec allows multipart but JSON-in-text is what LLMs handle best.
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
	if p.Name == "" {
		writeRPCError(w, req.ID, errInvalidParams, "name is required", nil)
		return
	}

	connectorID, opKey, err := h.resolveTool(r.Context(), p.Name, tagIDs, user.IsAdmin())
	if err != nil {
		writeRPCError(w, req.ID, errInvalidParams, err.Error(), nil)
		return
	}

	// Re-check authorization at call time — the cached tools/list
	// the client holds may be stale, and tags can change between
	// list and call.
	allowed, err := h.connectors.IsVisibleTo(r.Context(), connectorID, tagIDs, user.IsAdmin())
	if err != nil || !allowed {
		writeRPCError(w, req.ID, errInvalidParams, "tool not accessible", nil)
		return
	}

	input := stringifyArgs(p.Arguments)
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
		// IsError=true rather than a JSON-RPC error — the LLM should
		// see and reason about the message, not a transport failure.
		body := execErr.Error()
		if res != nil && res.ResponseJSON != "" {
			body = res.ResponseJSON
		}
		writeRPCResult(w, req.ID, toolCallResult{
			Content: []toolContent{{Type: "text", Text: body}},
			IsError: true,
		})
		return
	}
	writeRPCResult(w, req.ID, toolCallResult{
		Content: []toolContent{{Type: "text", Text: res.ResponseJSON}},
		IsError: false,
	})
}

// resolveTool inverts the toolName format back to (connector_id, op_key)
// by scanning the caller's visible rows for a (key, op, label-slug)
// match. Linear scan is fine — total connector × op count is small,
// and this is a per-call op only.
func (h *Handler) resolveTool(ctx context.Context, name string, tagIDs []string, isAdmin bool) (connectorID, opKey string, err error) {
	parts := strings.SplitN(name, "__", 3)
	if len(parts) != 3 {
		return "", "", fmt.Errorf("malformed tool name %q", name)
	}
	wantKey, wantOp, wantSlug := parts[0], parts[1], parts[2]

	rows, err := h.connectors.ListVisibleTo(ctx, tagIDs, isAdmin)
	if err != nil {
		return "", "", err
	}
	for _, row := range rows {
		if row.Key != wantKey {
			continue
		}
		if slugify(row.Label) != wantSlug {
			continue
		}
		mod, ok := h.connectors.Module(row.Key)
		if !ok {
			continue
		}
		for _, op := range mod.Operations {
			if op.Key == wantOp {
				return row.ID, op.Key, nil
			}
		}
	}
	return "", "", errors.New("unknown or inaccessible tool")
}

// stringifyArgs flattens the LLM's JSON arguments map (string|number|
// bool|null) into the string-keyed map connectors.Service.Execute
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
