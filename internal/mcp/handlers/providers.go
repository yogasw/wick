package handlers

import (
	"encoding/json"
	"net/http"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// WickListProviders handles the wick_list_providers tool.
func WickListProviders(w http.ResponseWriter, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	instances, err := provider.Load()
	if err != nil {
		rsp.ToolError(w, req.ID, "load providers: "+err.Error(), "wick_list_providers")
		return
	}

	activeKey := ""
	sessionID, _ := args["session_id"].(string)
	agentName, _ := args["agent_name"].(string)
	if sessionID != "" {
		if agentName == "" {
			agentName = "main"
		}
		if sess, err := session.Load(layout, sessionID); err == nil {
			for _, a := range sess.Agents {
				if a.Name == agentName {
					activeKey = a.Provider
					break
				}
			}
		}
	}

	type providerSummary struct {
		Type     string `json:"type"`
		Name     string `json:"name"`
		Binary   string `json:"binary,omitempty"`
		Disabled bool   `json:"disabled,omitempty"`
		Active   bool   `json:"active,omitempty"`
	}
	out := make([]providerSummary, 0, len(instances))
	for _, ins := range instances {
		key := string(ins.Type) + "/" + ins.Name
		out = append(out, providerSummary{
			Type:     string(ins.Type),
			Name:     ins.Name,
			Binary:   ins.Binary,
			Disabled: ins.Disabled,
			Active:   activeKey != "" && key == activeKey,
		})
	}
	b, _ := json.Marshal(map[string]any{"providers": out, "total": len(out)})
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
