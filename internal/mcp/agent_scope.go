package mcp

import (
	"context"
	"sync/atomic"

	"github.com/yogasw/wick/internal/connectors"
)

// AgentScopeResolver maps a session to the connector scope of the
// Agents-app agent it belongs to, or nil when the session is not an
// agent's (an ordinary session, a sub-agent of one, an unknown id).
type AgentScopeResolver func(ctx context.Context, sessionID string) connectors.AgentScope

var agentScopeResolver atomic.Pointer[AgentScopeResolver]

// SetAgentScopeResolver wires the resolver both MCP entry points consult
// once the calling session is known. Set at boot; nil switches scoping
// off, which is also the state before boot reaches it.
func SetAgentScopeResolver(f AgentScopeResolver) {
	if f == nil {
		agentScopeResolver.Store(nil)
		return
	}
	agentScopeResolver.Store(&f)
}

// withAgentScope attaches the agent scope for sessionID to ctx. Applied
// after the principal is resolved so the scope only ever narrows what that
// principal already reaches — connectors.Service checks it on top of the
// owner's own visibility, never instead of it.
func withAgentScope(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	p := agentScopeResolver.Load()
	if p == nil {
		return ctx
	}
	return connectors.WithAgentScope(ctx, (*p)(ctx, sessionID))
}
