package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// WickTodo handles the todo tool for CLI providers (claude/codex/gemini)
// reached over MCP — the same tool wick's own in-process engine exposes
// natively (internal/agents/provider/wick/tool_todo.go). Sharing one
// tool definition across every provider means the web UI's merged
// checklist widget (fe/agents/conversation/src/lib/todoGroups.ts) works
// identically no matter which provider is running: it groups tool_use
// events named "todo" out of the trace regardless of transport.
//
// No server-side persistence beyond the trace — like wick's native
// handler, this just validates and echoes a normalized summary; the
// plan/progress rows are already visible via the tool_use event itself.
func WickTodo(w http.ResponseWriter, req RPCRequest, rsp Responder, args map[string]any) {
	type itemIn struct {
		ID     string `json:"id,omitempty"`
		Step   string `json:"step"`
		Status string `json:"status"`
	}
	type input struct {
		Items []itemIn `json:"items"`
	}
	raw, _ := json.Marshal(args)
	var in input
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Items) == 0 {
		rsp.ToolError(w, req.ID, "items must be a non-empty array of {step, status}", "todo")
		return
	}

	var sb strings.Builder
	done := 0
	for _, it := range in.Items {
		mark := "[ ]"
		switch it.Status {
		case "completed":
			mark = "[x]"
			done++
		case "in_progress":
			mark = "[~]"
		}
		fmt.Fprintf(&sb, "%s %s\n", mark, it.Step)
	}
	fmt.Fprintf(&sb, "(%d/%d done)", done, len(in.Items))

	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: sb.String()}},
	})
}
