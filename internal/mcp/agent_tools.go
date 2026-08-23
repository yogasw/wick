package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// agent_tools.go exposes the MCP tool surface to the in-process wick
// provider agent. It reuses the SAME dispatch (dispatchTool) the CLI
// providers get over loopback, so an agent on the built-in wick provider
// can call every wick tool (ask_user, wick_set_title, wick_session_info,
// connectors, providers, skills, wickmanager, …) exactly as a claude agent
// would via MCP.
//
// Identity is per-CALLER, not fixed. The CLI providers authenticate with a
// per-session scoped token naming the human who owns the session; the
// in-process path has no HTTP hop to carry a bearer, so the caller passes
// the principal directly via AgentIdentity. A zero AgentIdentity falls back
// to the synthetic admin (auth.go internalSystemUser) for ownerless spawns
// — cron, system jobs, sessions predating ownership tracking.

// AgentIdentity is the principal an in-process agent tool call runs as.
// The zero value means "no resolved owner" and maps to the synthetic admin,
// matching the pre-per-user behaviour for ownerless spawns.
type AgentIdentity struct {
	// User is the resolved wick user. nil = synthetic admin fallback.
	User *entity.User
	// TagIDs is the caller's filter-tag set, applied to connector
	// visibility exactly as it is for an HTTP-authed request. nil for the
	// admin fallback (no filtering).
	TagIDs []string
}

// resolve returns the effective principal + tags for a call.
func (id AgentIdentity) resolve() (*entity.User, []string) {
	if id.User == nil {
		return internalSystemUser(), nil
	}
	return id.User, id.TagIDs
}

// AgentToolDescriptors returns the tool catalog the in-process agent may
// call, as the synthetic admin principal (full visibility). Kept for
// callers with no resolved owner; prefer AgentToolDescriptorsAs.
func (h *Handler) AgentToolDescriptors(ctx context.Context) []handlers.ToolDescriptor {
	return h.AgentToolDescriptorsAs(ctx, AgentIdentity{})
}

// AgentToolDescriptorsAs returns the tool catalog visible to one principal.
// Tag filtering and the admin flag follow that principal, so an in-process
// agent sees exactly the tools its owning human may reach — the same answer
// the HTTP path gives for the same user.
func (h *Handler) AgentToolDescriptorsAs(ctx context.Context, id AgentIdentity) []handlers.ToolDescriptor {
	user, tagIDs := id.resolve()
	tools := handlers.MetaToolDescriptors()
	tools = append(tools, handlers.WickManagerToolDescriptors(ctx, h.connectors, tagIDs, user.IsAdmin())...)
	tools = append(tools, handlers.SubAgentsToolDescriptors(ctx, h.connectors, tagIDs, user.IsAdmin())...)
	return tools
}

// CallAgentTool dispatches one tool call in-process and returns the tool
// result text + isError, reusing dispatchTool (identical routing to the
// HTTP transport). sessionID is threaded via the X-Wick-Session-Id header
// so session-aware tools (ask_user, wick_session_*) resolve correctly.
func (h *Handler) CallAgentTool(ctx context.Context, name string, args map[string]any, sessionID string) (string, bool) {
	return h.CallAgentToolAs(ctx, name, args, sessionID, AgentIdentity{})
}

// CallAgentToolAs dispatches one in-process tool call as a specific
// principal, so per-owner gates and tag filtering see the real human
// instead of a synthetic admin.
func (h *Handler) CallAgentToolAs(ctx context.Context, name string, args map[string]any, sessionID string, id AgentIdentity) (string, bool) {
	user, tagIDs := id.resolve()
	ctx = login.WithUser(ctx, user, tagIDs)

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

	h.dispatchTool(discardWriter{}, r, hreq, rsp, name, args, user, tagIDs)
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
