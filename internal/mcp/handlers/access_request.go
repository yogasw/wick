package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/accessreq"
	"github.com/yogasw/wick/internal/connectors"
)

// AccessRequests backs wick_request_access. Wired by the server; nil
// leaves the tool answering that access requests are not available.
var AccessRequests interface {
	Create(ctx context.Context, in accessreq.CreateInput) (accessreq.View, error)
	ViewFor(ctx context.Context, id, viewerID string) (accessreq.View, error)
	Cancel(ctx context.Context, id, sessionID string) error
}

const accessRequestTool = "wick_request_access"

// AccessRequestDescriptor is the wick_request_access descriptor.
// AccessRequestsOn reports whether a session's project turned access
// requests on (wired at boot). nil = off everywhere: the tool is not
// listed and refuses.
var AccessRequestsOn func(sessionID string) bool

// AccessRequestListed reports whether wick_request_access is shown to a
// session.
func AccessRequestListed(sessionID string) bool {
	return AccessRequestsOn != nil && sessionID != "" && AccessRequestsOn(sessionID)
}

func AccessRequestDescriptor() ToolDescriptor {
	return ToolDescriptor{
		Name: accessRequestTool,
		Description: "Ask a person to approve connector actions YOUR access cannot run (wick_execute refused them: " +
			"\"not allowed for this agent\", a read-only grant, a role that may only read, or an access rule that holds the call " +
			"for a person). Use it too for an action your access DOES allow when your instructions say that target needs a " +
			"person's approval first (a post to another team's channel, say): file it here instead of running it. One request carries up to 10 " +
			"exact actions ({tool_id, params}, the same shape as wick_execute). A card appears where this conversation lives " +
			"(the Slack thread, the web); an approver opens it, sees every action with its real parameters, and decides each " +
			"one: once, for 1 / 8 / 24 hours or for this session (same operation on the same target, and the same config_override, only), or deny (standing access is a rule, set in the project settings, Access requests). Approved actions run with the APPROVER's access — you do NOT run " +
			"them again. This call returns at once with status pending: say what you asked for and END YOUR TURN; the " +
			"outcome of every action is delivered to this conversation later. An approval holds for those exact parameters " +
			"only; changed parameters need a new request. Never ask for something the user did not want done. " +
			"To propose a project's access rule (only when a person asked for it), use tool_id conn:wick-access-rules/save_rule " +
			"with params {project_id, doc: the rule as YAML, rule_id: to change an existing one, reason}; it is saved only when " +
			"the project's owner or an admin approves it. " +
			"Works only where the session's project turned access requests on (project settings); elsewhere it refuses and a refused op simply stays refused. " +
			"action=status reads a request, action=cancel withdraws one. " +
			"When you tell the person, keep it to one line (\"waiting for approval\"): never repeat the request id, who can approve, the params or config_override; the card already shows them.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": []string{"create", "status", "cancel"},
					"description": "create (default), status or cancel."},
				"actions": map[string]any{
					"type":        "array",
					"description": "create: the actions to approve, in order.",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"tool_id"},
						"properties": map[string]any{
							"tool_id": map[string]any{"type": "string", "description": "conn:<connector_id>/<op_key> exactly as wick_execute takes it."},
							"params":  map[string]any{"type": "object", "description": "The exact parameters the action runs with. To run it with a few config keys set differently for this call only (when the connector lists them as overridable, e.g. an op off because mode is read), add config_override: {\"mode\": \"write\"} here; such an action is approved once, by someone who may configure the connector."},
						},
					},
				},
				"reason":     map[string]any{"type": "string", "description": "create (required): one or two plain sentences for the approver: WHAT each action changes (which record, field or draft, from what to what) and WHY (the case it fixes). Shown on the card as your claim, so the approver can decide without opening the web."},
				"request_id": map[string]any{"type": "string", "description": "status / cancel: the request id create returned."},
			},
		},
		Annotations: &ToolAnnotation{Title: "Request access", ReadOnlyHint: PtrBool(false)},
	}
}

// WickRequestAccess handles wick_request_access.
func WickRequestAccess(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, args map[string]any) {
	ctx := r.Context()
	sessionID := ResolveCallSession(SessionOf(r), "")
	if AccessRequests == nil {
		rsp.ToolError(w, req.ID, "access requests are not available on this wick", accessRequestTool)
		return
	}
	if sessionID == "" || !SessionTrusted(ctx, sessionID) {
		rsp.ToolError(w, req.ID, "access requests are only available from an agent session", accessRequestTool)
		return
	}
	action, _ := args["action"].(string)
	switch strings.TrimSpace(action) {
	case "", "create":
	case "status":
		id, _ := args["request_id"].(string)
		v, err := AccessRequests.ViewFor(ctx, strings.TrimSpace(id), "")
		if err == nil && v.SessionID != sessionID {
			err = accessreq.ErrNotFound
		}
		writeAccessResult(w, req, rsp, accessSummary(v), err)
		return
	case "cancel":
		id, _ := args["request_id"].(string)
		err := AccessRequests.Cancel(ctx, strings.TrimSpace(id), sessionID)
		writeAccessResult(w, req, rsp, map[string]any{"request_id": id, "status": accessreq.StatusCancelled}, err)
		return
	default:
		rsp.ToolError(w, req.ID, "action must be create, status or cancel", accessRequestTool)
		return
	}
	raw, _ := args["actions"].([]any)
	in := accessreq.CreateInput{SessionID: sessionID, AgentID: connectors.AgentIDOf(ctx)}
	in.Reason, _ = args["reason"].(string)
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		rsp.ToolError(w, req.ID, "reason is required: one or two sentences saying what the actions change and why, for the approver", accessRequestTool)
		return
	}
	for i, a := range raw {
		m, _ := a.(map[string]any)
		toolID, _ := m["tool_id"].(string)
		connectorID, opKey, accountID, err := ParseToolIDFull(strings.TrimSpace(toolID))
		if err != nil {
			rsp.ToolError(w, req.ID, "actions["+itoa(i)+"].tool_id: "+err.Error(), accessRequestTool)
			return
		}
		params, _ := m["params"].(map[string]any)
		in.Actions = append(in.Actions, accessreq.ActionInput{ConnectorID: connectorID, OpKey: opKey, AccountID: accountID, Params: params})
	}
	v, err := AccessRequests.Create(ctx, in)
	out := accessSummary(v)
	if err == nil {
		out["next"] = "Tell the person what you asked for and end your turn. The outcome arrives in this conversation; approved actions run on their own — do not run them yourself."
	}
	writeAccessResult(w, req, rsp, out, err)
}

// accessReplyNote tells the agent how to word its reply: the card already
// shows everything an approver needs, so repeating its id, who may approve
// or the raw parameters only leaks details into the conversation.
const accessReplyNote = "The card is already posted where this conversation lives. In your reply do NOT repeat the request id, " +
	"who can approve or decided, the params or any config_override. One short line is enough, e.g. \"Waiting for approval.\""

// accessSummary is the request as the agent sees it: outcomes and, at
// most, the approver group label from a rule. No person's name is
// returned: agents copied it into client threads.
func accessSummary(v accessreq.View) map[string]any {
	items := make([]map[string]any, 0, len(v.Items))
	var groups []string
	seen := map[string]bool{}
	for _, it := range v.Items {
		m := map[string]any{
			"n": it.Idx + 1, "tool": it.ConnectorKey + "." + it.OpKey, "status": it.Status,
		}
		if it.Target != "" {
			m["target"] = it.Target
		}
		if it.Mode != "" {
			m["mode"] = it.Mode
		}
		if it.Note != "" {
			m["note"] = it.Note
		}
		if it.Error != "" {
			m["error"] = it.Error
		}
		if it.Result != "" {
			m["result"] = it.Result
		}
		items = append(items, m)
		if g := strings.TrimSpace(it.ApproversText); g != "" && !seen[g] {
			seen[g] = true
			groups = append(groups, g)
		}
	}
	out := map[string]any{"request_id": v.ID, "status": v.Status, "pending": v.Pending, "actions": items, "reply_note": accessReplyNote}
	if len(groups) > 0 {
		out["approver_group"] = strings.Join(groups, "; ")
	}
	return out
}

func writeAccessResult(w http.ResponseWriter, req RPCRequest, rsp Responder, out any, err error) {
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), accessRequestTool)
		return
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
