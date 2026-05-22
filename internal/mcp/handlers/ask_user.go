package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/askuser"
)

// AskUser handles the ask_user tool.
// manager is nil when ask_user is disabled.
// allowed is the gate-policy check; nil means always allowed.
func AskUser(
	w http.ResponseWriter,
	r *http.Request,
	req RPCRequest,
	rsp Responder,
	manager *askuser.Manager,
	allowed func() (bool, string),
	args map[string]any,
) {
	if manager == nil {
		rsp.WriteError(w, req.ID, -32603, "ask_user disabled — wick is not running with the agents UI", nil)
		return
	}
	if allowed != nil {
		if ok, reason := allowed(); !ok {
			msg := "ask_user blocked by gate policy"
			if strings.TrimSpace(reason) != "" {
				msg += " (" + reason + ")"
			}
			msg += " — pick a sensible default and proceed without prompting the user."
			rsp.ToolError(w, req.ID, msg, "ask_user")
			return
		}
	}

	type optionIn struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	type input struct {
		SessionID     string     `json:"session_id"`
		AgentName     string     `json:"agent_name"`
		Question      string     `json:"question"`
		Options       []optionIn `json:"options"`
		AllowFreeform bool       `json:"allow_freeform"`
	}
	raw, _ := json.Marshal(args)
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		rsp.ToolError(w, req.ID, "invalid arguments: "+err.Error(), "ask_user")
		return
	}
	if strings.TrimSpace(in.SessionID) == "" {
		rsp.ToolError(w, req.ID, "session_id is required", "ask_user")
		return
	}
	if strings.TrimSpace(in.Question) == "" {
		rsp.ToolError(w, req.ID, "question is required", "ask_user")
		return
	}
	opts := make([]askuser.Option, 0, len(in.Options))
	for _, o := range in.Options {
		opts = append(opts, askuser.Option{Label: o.Label, Value: o.Value})
	}

	q := askuser.Question{
		SessionID:     in.SessionID,
		AgentName:     in.AgentName,
		Question:      in.Question,
		Options:       opts,
		AllowFreeform: in.AllowFreeform,
		Timeout:       4 * time.Minute,
	}

	done := make(chan struct{})
	if r != nil {
		ctx := r.Context()
		go func() {
			<-ctx.Done()
			close(done)
		}()
	}

	ans, err := manager.Ask(q, done)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), "ask_user")
		return
	}
	out := map[string]string{"value": ans.Value, "text": ans.Text}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
