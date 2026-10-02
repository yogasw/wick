package connectors

import (
	"context"

	"github.com/yogasw/wick/internal/entity"
)

// AgentScope narrows what an agent (Agents app) may reach on top of its
// owner's own visibility. It can only take away: every check here runs
// AFTER the tag/ownership/admin rules have already decided the owner may
// see the row, so a scope can never grant a connector the owner lacks.
//
// The scope rides on the request context. The MCP layer attaches it when
// the calling session belongs to an agent (see mcp.SetAgentScopeResolver);
// every other caller has no scope and behaves exactly as before.
type AgentScope interface {
	// AllowConnector reports whether the connector instance is on the
	// agent's checklist at all.
	AllowConnector(connectorID string) bool
	// AllowAccount reports whether the agent may run the connector as the
	// given connected account. accountID "" is the instance's own
	// identity (the bot), which the checklist treats as one more account.
	AllowAccount(connectorID, accountID string) bool
	// AllowOp reports whether one operation is permitted. destructive is
	// the op's own declaration, used by the "read only" level.
	AllowOp(connectorID, opKey string, destructive bool) bool
}

type agentScopeKey struct{}

// WithAgentScope attaches scope to ctx. A nil scope leaves ctx unchanged.
func WithAgentScope(ctx context.Context, scope AgentScope) context.Context {
	if scope == nil {
		return ctx
	}
	return context.WithValue(ctx, agentScopeKey{}, scope)
}

// WithoutAgentScope returns ctx with any agent scope removed, for the
// surfaces that must see a person's own reach even when called from inside
// an agent session (the checklist an agent is narrowed FROM).
func WithoutAgentScope(ctx context.Context) context.Context {
	if AgentScopeFrom(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, agentScopeKey{}, nil)
}

// AgentScopeFrom returns the scope on ctx, or nil when the caller is not
// an agent.
func AgentScopeFrom(ctx context.Context) AgentScope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(agentScopeKey{}).(AgentScope)
	return s
}

// filterRowsByScope drops rows the agent scope does not list.
func filterRowsByScope(ctx context.Context, rows []entity.Connector) []entity.Connector {
	scope := AgentScopeFrom(ctx)
	if scope == nil {
		return rows
	}
	out := rows[:0:0]
	for _, r := range rows {
		if scope.AllowConnector(r.ID) {
			out = append(out, r)
		}
	}
	return out
}

// filterAccountsByScope drops connected accounts the agent scope does not
// list for this row.
func filterAccountsByScope(ctx context.Context, connectorID string, accs []entity.ConnectorAccount) []entity.ConnectorAccount {
	scope := AgentScopeFrom(ctx)
	if scope == nil {
		return accs
	}
	out := accs[:0:0]
	for _, a := range accs {
		if scope.AllowAccount(connectorID, a.ID) {
			out = append(out, a)
		}
	}
	return out
}
