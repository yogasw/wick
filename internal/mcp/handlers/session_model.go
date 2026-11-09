package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/login"
)

const sessionModelDescription = "Pin this conversation to a provider + model, or clear the pin. " +
	"action=get shows the provider, model and pin the session runs on and the default it returns to. " +
	"action=set provider=<type/name> model=<id> pins it: every later turn runs there, over the agent's default " +
	"(even when the agent's Settings lock chats to the default), until cleared. " +
	"action=clear removes the pin and puts the session back on its agent's (or project's) default. " +
	"set and clear apply from the NEXT turn: the turn you are in finishes on what it started on. " +
	"No session_id: this conversation is resolved from the call."

// SessionModelDescriptor is the wick_session_model tool.
func SessionModelDescriptor() ToolDescriptor {
	return ToolDescriptor{
		Name:        "wick_session_model",
		Description: sessionModelDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "enum": []string{"get", "set", "clear"},
					"description": "get (default) | set | clear.",
				},
				"provider": map[string]any{
					"type":        "string",
					"description": "set: provider instance key, \"type/name\" (a bare type means its default instance), e.g. \"claude/claude\". See wick_list_providers.",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "set: model id on that provider, e.g. \"claude-opus-5-5\". Omit for the provider's own default model.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Optional. Another session (you must own it, or be an admin). Omit for the current one.",
				},
			},
		},
		Annotations: &ToolAnnotation{
			Title:           "Session provider + model",
			ReadOnlyHint:    PtrBool(false),
			DestructiveHint: PtrBool(false),
			IdempotentHint:  PtrBool(true),
		},
	}
}

// WickSessionModel handles wick_session_model: get, set (pin) or clear
// the provider + model of one session. The switch is persisted only; the
// pool respawns an idle process on the new pair at the session's next
// message, so the caller's own turn is never cut short.
func WickSessionModel(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	const tool = "wick_session_model"
	own := SessionOf(r)
	arg, _ := args["session_id"].(string)
	sessionID := ResolveSessionPreferArg(own, arg)
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required (no session on the call)", tool)
		return
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), tool)
		return
	}
	// The call's own session needs no ownership check: an agent may always
	// pin or release the conversation it is running in.
	if sessionID != own && !canManageSession(login.GetUser(r.Context()), sess.Meta.UserID) {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), tool)
		return
	}
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	var out map[string]any
	switch action {
	case "", "get":
		out = sessionModelState(r, sess)
	case "set":
		prov, _ := args["provider"].(string)
		model, _ := args["model"].(string)
		res, err := provider.PinSession(r.Context(), layout, sessionID, "", prov, model, provider.SwitchOptions{Source: "agent"})
		if err != nil {
			rsp.ToolError(w, req.ID, "set: "+err.Error(), tool)
			return
		}
		out = pinOut(res)
	case "clear":
		res, err := provider.UnpinSession(r.Context(), layout, sessionID, "", provider.SwitchOptions{Source: "agent"})
		if err != nil {
			rsp.ToolError(w, req.ID, "clear: "+err.Error(), tool)
			return
		}
		out = pinOut(res)
	default:
		rsp.ToolError(w, req.ID, fmt.Sprintf("unknown action %q: use get, set or clear", action), tool)
		return
	}
	out["session_id"] = sessionID
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}

// sessionModelState is action=get: the primary agent's pair and pin, and
// the default a clear would return it to.
func sessionModelState(r *http.Request, sess session.Session) map[string]any {
	name := session.PrimaryAgentName(sess)
	out := map[string]any{"agent": name, "pinned": false}
	for _, a := range sess.Agents {
		if a.Name == name {
			out["provider"], out["model"], out["pinned"] = a.Provider, a.ModelID, a.Pinned
			break
		}
	}
	if provider.SessionDefault != nil {
		if tag, model, ok := provider.SessionDefault(r.Context(), sess.ID); ok {
			out["default_provider"], out["default_model"] = tag, model
		}
	}
	return out
}

func pinOut(res provider.PinResult) map[string]any {
	out := map[string]any{
		"agent":    res.Agent,
		"provider": res.Provider,
		"model":    res.Model,
		"pinned":   res.Pinned,
		"switched": res.Switched,
	}
	if res.Switched {
		out["applies"] = "next turn"
	}
	return out
}
