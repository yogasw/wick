package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// agent_tools.go exposes the MCP tool surface to the in-process wick
// provider agent. It reuses the SAME dispatch (dispatchTool) and the SAME
// identity the CLI providers get over loopback — the synthetic admin
// principal the internal token maps to (see auth.go internalSystemUser) —
// so an agent on the built-in wick provider can call every wick tool
// (ask_user, wick_set_title, wick_session_info, connectors, providers,
// skills, wickmanager, …) exactly as a claude/codex agent would via MCP,
// with no new auth surface.

// AgentToolDescriptors returns the tool catalog the in-process agent may
// call, as the synthetic admin principal (full visibility, like the CLI
// agents' loopback MCP calls). Includes the dynamic wickmanager tools.
func (h *Handler) AgentToolDescriptors(ctx context.Context) []handlers.ToolDescriptor {
	user := internalSystemUser()
	tools := handlers.MetaToolDescriptors()
	tools = append(tools, handlers.WickManagerToolDescriptors(ctx, h.connectors, nil, user.IsAdmin())...)
	return tools
}

// CallAgentTool dispatches one tool call in-process and returns the tool
// result text + isError, reusing dispatchTool (identical routing to the
// HTTP transport). sessionID is threaded via the X-Wick-Session-Id header
// so session-aware tools (ask_user, wick_session_*) resolve correctly.
func (h *Handler) CallAgentTool(ctx context.Context, name string, args map[string]any, sessionID string) (string, bool) {
	user := internalSystemUser()
	ctx = login.WithUser(ctx, user, nil)

	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/mcp", nil)
	if sessionID != "" {
		r.Header.Set("X-Wick-Session-Id", sessionID)
	}

	var captured string
	var isErr bool
	rsp := handlers.Responder{
		WriteResult: func(_ http.ResponseWriter, _ json.RawMessage, result any) {
			if tr, ok := result.(handlers.ToolCallResult); ok {
				isErr = tr.IsError
				if len(tr.Content) > 0 {
					captured = tr.Content[0].Text
				}
			}
		},
		WriteError: func(_ http.ResponseWriter, _ json.RawMessage, _ int, message string, _ any) {
			isErr = true
			captured = message
		},
	}

	// hreq params carry the tool name + arguments so handlers that re-read
	// Params (rather than the args map) still see them.
	paramsJSON, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	hreq := handlers.RPCRequest{ID: json.RawMessage("0"), Params: paramsJSON}

	h.dispatchTool(discardWriter{}, r, hreq, rsp, name, args, user, nil)
	return captured, isErr
}

// discardWriter is a no-op http.ResponseWriter. The tool handlers route
// their output through the capturing Responder above; the writer they are
// handed is only passed through and never written to on the in-process
// path, so discarding is safe.
type discardWriter struct{}

func (discardWriter) Header() http.Header         { return http.Header{} }
func (discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (discardWriter) WriteHeader(int)             {}
