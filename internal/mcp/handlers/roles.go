package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// RolesRead and RolesWrite back wick_roles / wick_roles_set: a Team
// agent reading and changing its OWN role document (By role access).
// They are wired by the server; nil leaves the tools answering that roles
// are not available. Both resolve the agent from the session, never from
// an argument, so an agent can only ever touch its own roles.
var (
	// RolesRead returns the session's agent's roles as the asker may see
	// them: the whole document for its owner, the asker's own role for
	// anyone else.
	RolesRead func(ctx context.Context, sessionID string) (any, error)
	// RolesWrite saves a new role document; it refuses unless the agent's
	// owner is the person asking in sessionID.
	RolesWrite func(ctx context.Context, sessionID, doc string, baseRev int, reason string) (any, error)
)

const rolesDocDesc = "The WHOLE new role document as JSON, edited from wick_roles: " +
	`{"roles":{"<name>":{"rank":1-99,"lend":"off|read|all","write_needs_approval":bool}},` +
	`"rules":[{"match":"user|shared","value":"<wick user id>","role":"<role>"}],` +
	`"lend":{"<connector_id>":{"<role or guest>":"off|read|all" or {"level":"pick","ops":[...]}}}}. ` +
	"owner and guest are built in and never defined under roles. A rule may give role owner (a co-owner, " +
	"borrows every connector of the agent) or guest. guest has no default: it borrows only where a lend row names guest."

// RolesDescriptors are the wick_roles / wick_roles_set descriptors.
func RolesDescriptors() []ToolDescriptor {
	return []ToolDescriptor{
		{
			Name: "wick_roles",
			Description: "Who is who on THIS agent (By role access). Returns your_role: the role of the person " +
				"who sent the current message (owner, a role of the owner's, or guest), and enabled (whether By role is on). " +
				"For the agent's owner it also returns the whole role document (roles, rules mapping people to roles, " +
				"the lend table per connector) with its rev and recent changes. Use it to answer \"what is my role here\" " +
				"and before wick_roles_set.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Annotations: &ToolAnnotation{Title: "Read roles", ReadOnlyHint: PtrBool(true)},
		},
		{
			Name: "wick_roles_set",
			Description: "Save THIS agent's role document (read with wick_roles, edited) as a new revision: make someone " +
				"owner (co-owner), trusted, member or guest, or lend a connector to a role or to guest. Only when the agent's " +
				"owner is the person asking in this chat; refused for anyone else, a teammate, a schedule or a forwarded message. " +
				"Applies on the next call. Undo = save an older revision's document again.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"doc", "base_rev"},
				"properties": map[string]any{
					"doc":      map[string]any{"type": "string", "description": rolesDocDesc},
					"base_rev": map[string]any{"type": "integer", "description": "The rev wick_roles returned. A save made from an older rev is refused so nobody's change is overwritten."},
					"reason":   map[string]any{"type": "string", "description": "Why, kept in the role history."},
				},
			},
			Annotations: &ToolAnnotation{Title: "Change roles", ReadOnlyHint: PtrBool(false), DestructiveHint: PtrBool(true)},
		},
	}
}

// WickRoles handles wick_roles.
func WickRoles(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder) {
	sessionID := ResolveSessionPreferArg(SessionOf(r), "")
	if RolesRead == nil || sessionID == "" {
		rsp.ToolError(w, req.ID, "roles are only available in a Team agent's session", "wick_roles")
		return
	}
	out, err := RolesRead(r.Context(), sessionID)
	writeRolesResult(w, req, rsp, "wick_roles", out, err)
}

// WickRolesSet handles wick_roles_set.
func WickRolesSet(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, args map[string]any) {
	sessionID := ResolveSessionPreferArg(SessionOf(r), "")
	if RolesWrite == nil || sessionID == "" {
		rsp.ToolError(w, req.ID, "roles are only available in a Team agent's session", "wick_roles_set")
		return
	}
	doc, _ := args["doc"].(string)
	reason, _ := args["reason"].(string)
	rev, ok := revArg(args["base_rev"])
	if !ok {
		rsp.ToolError(w, req.ID, "base_rev must be the rev number wick_roles returned", "wick_roles_set")
		return
	}
	out, err := RolesWrite(r.Context(), sessionID, doc, rev, reason)
	writeRolesResult(w, req, rsp, "wick_roles_set", out, err)
}

func writeRolesResult(w http.ResponseWriter, req RPCRequest, rsp Responder, tool string, out any, err error) {
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), tool)
		return
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}

// revArg reads a JSON number or numeric string.
func revArg(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	}
	return 0, false
}
