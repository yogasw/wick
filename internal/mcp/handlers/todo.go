package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// WickTodo handles the shared `todo` tool for every provider (claude/codex/
// gemini over MCP, wick in-process via CallAgentTool). One surface for
// checklist progress AND the optional goal latch — no separate goal tool.
//
// Checklist: validates + echoes a normalized summary (UI groups tool_use
// events named "todo"). Goal mode (optional fields):
//
//	goal          — open/replace the durable latch (<SessionDir>/goal.json)
//	goal_done     — mark latch done (releases wick force-continue)
//	goal_abandon  — mark latch abandoned
//
// Goal writes need a session id (X-Wick-Session-Id header or session_id
// arg) + layout. Missing either → checklist still works; goal fields
// return a clear error so the model knows why the latch didn't stick.
func WickTodo(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	type substepIn struct {
		Step   string `json:"step"`
		Status string `json:"status"`
	}
	type itemIn struct {
		ID          string      `json:"id,omitempty"`
		Title       string      `json:"title,omitempty"`
		Description string      `json:"description,omitempty"`
		Step        string      `json:"step,omitempty"` // deprecated fallback label, see tools.go
		Status      string      `json:"status"`
		Substeps    []substepIn `json:"substeps,omitempty"`
	}
	type input struct {
		Items       []itemIn `json:"items"`
		Goal        string   `json:"goal,omitempty"`
		GoalDone    bool     `json:"goal_done,omitempty"`
		GoalAbandon bool     `json:"goal_abandon,omitempty"`
		Note        string   `json:"note,omitempty"`
		SessionID   string   `json:"session_id,omitempty"`
	}
	raw, _ := json.Marshal(args)
	var in input
	if err := json.Unmarshal(raw, &in); err != nil || len(in.Items) == 0 {
		rsp.ToolError(w, req.ID, "items must be a non-empty array of {title, status}", "todo")
		return
	}

	mark := func(status string) string {
		switch status {
		case "completed":
			return "[x]"
		case "in_progress":
			return "[~]"
		default:
			return "[ ]"
		}
	}

	var sb strings.Builder
	done := 0
	for _, it := range in.Items {
		if it.Status == "completed" {
			done++
		}
		label := it.Title
		if label == "" {
			label = it.Step
		}
		fmt.Fprintf(&sb, "%s %s\n", mark(it.Status), label)
		for _, sub := range it.Substeps {
			fmt.Fprintf(&sb, "    %s %s\n", mark(sub.Status), sub.Step)
		}
	}
	fmt.Fprintf(&sb, "(%d/%d done)", done, len(in.Items))

	// Goal latch — optional. All providers write the same file; only the
	// wick engine force-continues while open. Resolve session id the same
	// way connector tools do (arg, then header).
	wantGoal := strings.TrimSpace(in.Goal) != "" || in.GoalDone || in.GoalAbandon
	if wantGoal {
		sid := strings.TrimSpace(in.SessionID)
		if sid == "" && r != nil {
			sid = strings.TrimSpace(r.Header.Get("X-Wick-Session-Id"))
		}
		if sid == "" {
			fmt.Fprintf(&sb, "\n[goal] skipped — no session id (pass session_id or call from an agent session)")
		} else if layout.BaseDir == "" {
			fmt.Fprintf(&sb, "\n[goal] skipped — session layout not wired")
		} else {
			note := strings.TrimSpace(in.Note)
			switch {
			case in.GoalAbandon:
				if err := session.AbandonGoal(layout, sid, note); err != nil {
					fmt.Fprintf(&sb, "\n[goal] abandon failed: %s", err.Error())
				} else {
					fmt.Fprintf(&sb, "\n[goal] ABANDONED (turn may end)")
				}
			case in.GoalDone:
				if err := session.CompleteGoal(layout, sid, note); err != nil {
					fmt.Fprintf(&sb, "\n[goal] complete failed: %s", err.Error())
				} else {
					fmt.Fprintf(&sb, "\n[goal] DONE (turn may end)")
				}
			default:
				goal := strings.TrimSpace(in.Goal)
				if err := session.OpenGoal(layout, sid, goal, note); err != nil {
					fmt.Fprintf(&sb, "\n[goal] open failed: %s", err.Error())
				} else {
					fmt.Fprintf(&sb, "\n[goal] OPEN: %s\n(wick keeps the turn running until todo{goal_done:true} or todo{goal_abandon:true})", goal)
				}
			}
		}
	}

	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: sb.String()}},
	})
}
