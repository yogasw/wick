package handlers

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/yogasw/wick/internal/login"
)

// localCLIPrincipal is set only in the stdio process. atomic.Value because it
// is written once at startup and read per tool call.
var localCLIPrincipal atomic.Value

// WickMe handles the wick_me tool: who is this MCP call authenticated as.
//
// It reports the principal the server RESOLVED from the caller's credential,
// never anything the caller asserted. That distinction is the point of the
// tool: an agent's MCP identity comes from the token in its spawn argv, so
// the only trustworthy answer to "which user am I acting for" lives
// server-side. An agent that guesses from conversation text will eventually
// guess wrong — after a handover, or in a session someone else opened.
//
// is_system marks the synthetic admin principal wick falls back to when a
// spawn has no resolved owner (cron, system jobs, sessions predating
// ownership tracking). Callers should treat that as "no human attached"
// rather than as a real account.
func WickMe(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder) {
	user := login.GetUser(r.Context())
	if user == nil {
		// Unauthenticated transports (stdio / local tooling) have no
		// principal at all. Say so plainly instead of inventing one.
		b, _ := json.Marshal(map[string]any{
			"authenticated": false,
			"is_system":     false,
		})
		rsp.WriteResult(w, req.ID, ToolCallResult{
			Content: []ToolContent{{Type: "text", Text: string(b)}},
		})
		return
	}

	out := map[string]any{
		"authenticated": true,
		"user_id":       user.ID,
		"name":          user.Name,
		"email":         user.Email,
		"role":          string(user.Role),
		"is_admin":      user.IsAdmin(),
		"is_owner":      user.IsOwner,
		"approved":      user.Approved,
		// Tag ids drive connector visibility, so surfacing them lets an agent
		// explain WHY a connector it expected is missing instead of insisting
		// it should be there.
		"filter_tag_ids": login.GetUserTagIDs(r.Context()),
		"is_system":      user.ID == internalAgentUserID,
	}
	// The stdio transport (`wick mcp serve`) has no request and no token: it
	// binds to the first admin on the box, because wick_enc_ tokens are keyed
	// HKDF(masterKey, salt=user.ID) and a synthetic salt would mint tokens
	// nobody can decrypt. So the principal is "whoever runs the CLI", not a
	// user who authenticated. Say that outright — an agent told it is Root
	// would otherwise report a machine-local fallback as a verified human.
	if id, ok := localAdminPrincipal(); ok && user.ID == id {
		out["is_local_cli"] = true
		out["identity_source"] = "local-cli-fallback"
	} else {
		out["is_local_cli"] = false
		out["identity_source"] = "token"
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}

// internalAgentUserID mirrors mcp.InternalAgentUserID. It is duplicated as a
// constant here because internal/mcp imports this package, so importing back
// would create a cycle. The mcp package pins the pair with a test.
const internalAgentUserID = "wick-agent-internal"

// localAdminPrincipal reports the id the stdio transport bound to, when this
// process IS that transport. Set by the stdio entrypoint; unset (ok=false) in
// the HTTP server, where every caller presents a real credential.
func localAdminPrincipal() (string, bool) {
	v := localCLIPrincipal.Load()
	if v == nil {
		return "", false
	}
	id, _ := v.(string)
	return id, id != ""
}

// SetLocalCLIPrincipal marks this process as the stdio transport and records
// which user it bound to, so wick_me can distinguish a machine-local fallback
// from an authenticated user. Called once by the stdio entrypoint.
func SetLocalCLIPrincipal(userID string) { localCLIPrincipal.Store(userID) }

// InternalAgentUserIDForTest exposes the local copy so internal/mcp can pin it
// against its own constant (that package imports this one, so the check cannot
// live on the other side).
func InternalAgentUserIDForTest() string { return internalAgentUserID }
