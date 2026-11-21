package connectors

import (
	"context"
	"fmt"

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

// AgentFeatureScope is the optional half of an AgentScope that switches
// whole features off for an agent: every instance of a connector type,
// and wick's own MCP tools by name. An AgentScope without it gates
// nothing by feature.
type AgentFeatureScope interface {
	// AllowKey reports whether connectors of this type are on at all.
	AllowKey(connectorKey string) bool
	// AllowTool reports whether a top-level MCP tool may be listed and
	// called.
	AllowTool(name string) bool
}

// KeyAllowed reports whether the agent scope on ctx, if any, leaves the
// connector type key switched on.
func KeyAllowed(ctx context.Context, key string) bool {
	fs, ok := AgentScopeFrom(ctx).(AgentFeatureScope)
	return !ok || fs.AllowKey(key)
}

// ToolAllowed reports whether the agent scope on ctx, if any, leaves the
// MCP tool name switched on.
func ToolAllowed(ctx context.Context, name string) bool {
	fs, ok := AgentScopeFrom(ctx).(AgentFeatureScope)
	return !ok || fs.AllowTool(name)
}

// LendingScope is the optional part of an AgentScope that lends some
// connectors from the agent's owner: a person chatting who cannot reach a
// lent connector on their own runs it as the owner instead. Someone who
// does reach it keeps their own identity.
type LendingScope interface {
	// LentBy names the user lending connectorID, false when it is not lent.
	LentBy(connectorID string) (lenderID string, ok bool)
	// LentIDs lists every lent connector instance.
	LentIDs() []string
}

// RoleScope is the optional part of an AgentScope whose access depends on
// who is calling (an agent in "By role" access). callerID is the person
// asking, before any lend swaps in the lender.
type RoleScope interface {
	// CallerLevel is what callerID may do on connectorID: on their own
	// access (lent false) or on the owner's lend (lent true). level is
	// "all", "read", "pick" (ops lists the ops) or "off"; why says, in
	// words for the caller, what limited them ("" when nothing did).
	CallerLevel(ctx context.Context, callerID, connectorID string, lent bool) (level string, ops []string, why string)
}

// callerLevel asks the RoleScope on ctx, if any; no RoleScope permits all.
func callerLevel(ctx context.Context, callerID, connectorID string, lent bool) (string, []string, string) {
	rs, ok := AgentScopeFrom(ctx).(RoleScope)
	if !ok {
		return "all", nil, ""
	}
	return rs.CallerLevel(ctx, callerID, connectorID, lent)
}

// roleAllowsOp refuses an op the caller's role does not allow on
// connectorID (see RoleScope), with the reason in words.
func roleAllowsOp(ctx context.Context, callerID, connectorID, opKey string, destructive, lent bool) error {
	lv, ops, why := callerLevel(ctx, callerID, connectorID, lent)
	refuse := func(def string) error {
		if why == "" {
			why = def
		}
		return fmt.Errorf("operation %q is not allowed: %s", opKey, why)
	}
	switch lv {
	case "all":
		return nil
	case "read":
		if destructive {
			return refuse("your role may only read this connector")
		}
		return nil
	case "pick":
		for _, k := range ops {
			if k == opKey {
				return nil
			}
		}
		return refuse("your role may only run some operations of this connector")
	}
	return refuse("your role has no access to this connector")
}

// LentAccountScope is the optional part of a LendingScope that names the
// one account a lend runs as. A lend that lists exactly one account
// (not the instance's own identity) runs as it, instead of guessing
// the account from whoever is calling.
type LentAccountScope interface {
	LentAccount(connectorID string) (accountID string, ok bool)
}

// lentAccountID is the account a lent connector runs as, "" when the
// scope names none.
func lentAccountID(ctx context.Context, connectorID string) string {
	ls, ok := AgentScopeFrom(ctx).(LentAccountScope)
	if !ok {
		return ""
	}
	id, ok := ls.LentAccount(connectorID)
	if !ok {
		return ""
	}
	return id
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

// WithoutLending returns scope with its lending half removed: the same
// checklist and feature switches, but nothing lent. For a caller that
// named a session it may not use — the checklist still narrows them, the
// owner's lend must not widen them. nil stays nil.
func WithoutLending(scope AgentScope) AgentScope {
	if scope == nil {
		return nil
	}
	if fs, ok := scope.(AgentFeatureScope); ok {
		return unlentFeatureScope{unlentScope{scope}, fs}
	}
	return unlentScope{scope}
}

// unlentScope forwards only the AgentScope methods, so a type assertion
// to LendingScope or LentAccountScope fails on it.
type unlentScope struct{ s AgentScope }

func (u unlentScope) AllowConnector(id string) bool { return u.s.AllowConnector(id) }
func (u unlentScope) AllowAccount(id, accountID string) bool {
	return u.s.AllowAccount(id, accountID)
}
func (u unlentScope) AllowOp(id, opKey string, destructive bool) bool {
	return u.s.AllowOp(id, opKey, destructive)
}

type unlentFeatureScope struct {
	unlentScope
	fs AgentFeatureScope
}

func (u unlentFeatureScope) AllowKey(key string) bool   { return u.fs.AllowKey(key) }
func (u unlentFeatureScope) AllowTool(name string) bool { return u.fs.AllowTool(name) }

// agentIDOf is the Team agent behind the scope on ctx, "" when the scope
// does not name one.
func agentIDOf(ctx context.Context) string {
	if a, ok := AgentScopeFrom(ctx).(interface{ AgentID() string }); ok {
		return a.AgentID()
	}
	return ""
}

// AgentIDOf is the Team agent whose scope is on ctx, "" when none.
func AgentIDOf(ctx context.Context) string { return agentIDOf(ctx) }

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
	fs, _ := scope.(AgentFeatureScope)
	for _, r := range rows {
		if fs != nil && !fs.AllowKey(r.Key) {
			continue
		}
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
