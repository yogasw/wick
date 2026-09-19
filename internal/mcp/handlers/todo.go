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
	type progressIn struct {
		Done  int    `json:"done"`
		Total int    `json:"total"`
		Label string `json:"label,omitempty"`
	}
	type detailIn struct {
		Format string `json:"format,omitempty"`
		Body   string `json:"body"`
	}
	type itemIn struct {
		ID          string      `json:"id,omitempty"`
		Title       string      `json:"title,omitempty"`
		Description string      `json:"description,omitempty"`
		Step        string      `json:"step,omitempty"` // deprecated fallback label, see tools.go
		Status      string      `json:"status"`
		Substeps    []substepIn `json:"substeps,omitempty"`
		Progress    *progressIn `json:"progress,omitempty"`
		Detail      *detailIn   `json:"detail,omitempty"`
	}
	type input struct {
		Items       []itemIn `json:"items"`
		Goal        string   `json:"goal,omitempty"`
		GoalDone    bool     `json:"goal_done,omitempty"`
		GoalAbandon bool     `json:"goal_abandon,omitempty"`
		Note        string   `json:"note,omitempty"`
		SessionID   string   `json:"session_id,omitempty"`
		Stop        bool     `json:"stop,omitempty"`
		ClearHist   bool     `json:"clear_history,omitempty"`
	}
	raw, _ := json.Marshal(args)
	var in input
	if err := json.Unmarshal(raw, &in); err != nil {
		rsp.ToolError(w, req.ID, "items must be a non-empty array of {title, status}", "todo")
		return
	}
	// A goal-only call is legal: the engine tells the model to release the
	// latch with todo{goal_done:true} and nothing else (see
	// internal/agents/provider/wick/engine.go), so requiring items here
	// would reject the exact call we asked for. Items are still required
	// when the call touches no goal field at all.
	goalOnly := strings.TrimSpace(in.Goal) != "" || in.GoalDone || in.GoalAbandon
	// stop/clear_history act on the list that is already there, so they are
	// legal with no items too — requiring a checklist to say "this one is
	// dead" would mean re-sending the list you are abandoning.
	listOp := in.Stop || in.ClearHist
	if len(in.Items) == 0 && !goalOnly && !listOp {
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
	// Goal-only calls have no checklist to summarize — "(0/0 done)" would
	// read as an empty task list rather than "this call was about the goal".
	if len(in.Items) > 0 {
		fmt.Fprintf(&sb, "(%d/%d done)", done, len(in.Items))
	}

	// Persist the checklist. Echoing it was enough to draw a card in the
	// trace and nothing else: after a reload the live list is wherever it
	// landed in the scrollback, and a second person in the session cannot
	// see what is being worked on at all. Written on a best-effort basis —
	// a tool call must not fail because a file could not be written.
	if len(in.Items) > 0 && layout.BaseDir != "" {
		if sid := resolveTodoSession(r, in.SessionID); sid != "" {
			items := make([]session.TodoItem, 0, len(in.Items))
			for _, it := range in.Items {
				subs := make([]session.TodoSubstep, 0, len(it.Substeps))
				for _, sub := range it.Substeps {
					subs = append(subs, session.TodoSubstep{Step: sub.Step, Status: sub.Status})
				}
				out := session.TodoItem{
					ID:          it.ID,
					Title:       it.Title,
					Description: it.Description,
					Step:        it.Step,
					Status:      it.Status,
					Substeps:    subs,
				}
				if it.Progress != nil {
					out.Progress = &session.TodoProgress{
						Done: it.Progress.Done, Total: it.Progress.Total, Label: it.Progress.Label,
					}
				}
				if it.Detail != nil && strings.TrimSpace(it.Detail.Body) != "" {
					out.Detail = &session.TodoDetail{Format: it.Detail.Format, Body: it.Detail.Body}
				}
				items = append(items, out)
			}
			if _, err := session.RecordTodos(layout, sid, items); err != nil {
				fmt.Fprintf(&sb, "\n[todo] not saved: %s", err.Error())
			}
		}
	}

	// stop / clear_history — the escape hatch for a list nobody will finish.
	// A run that died leaves a card claiming to be in progress forever, and
	// the only thing worse than no progress indicator is one that lies.
	if listOp {
		sid := resolveTodoSession(r, in.SessionID)
		switch {
		case sid == "":
			fmt.Fprintf(&sb, "\n[todo] skipped — no session id (pass session_id or call from an agent session)")
		case layout.BaseDir == "":
			fmt.Fprintf(&sb, "\n[todo] skipped — session layout not wired")
		default:
			if in.Stop {
				if _, err := session.StopTodos(layout, sid, strings.TrimSpace(in.Note)); err != nil {
					fmt.Fprintf(&sb, "\n[todo] stop failed: %s", err.Error())
				} else {
					fmt.Fprintf(&sb, "\n[todo] list STOPPED")
				}
			}
			if in.ClearHist {
				if _, err := session.ClearTodos(layout, sid, false); err != nil {
					fmt.Fprintf(&sb, "\n[todo] clear failed: %s", err.Error())
				} else {
					fmt.Fprintf(&sb, "\n[todo] history cleared")
				}
			}
		}
	}

	// Goal latch — optional. All providers write the same file; only the
	// wick engine force-continues while open. Resolve session id the same
	// way connector tools do (arg, then header).
	if goalOnly {
		sid := resolveTodoSession(r, in.SessionID)
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

	// TrimSpace because the goal lines are written with a leading "\n" to
	// separate them from the checklist; without a checklist that would
	// leave the reply starting on a blank line.
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: strings.TrimSpace(sb.String())}},
	})
}

// resolveTodoSession finds the session a todo call belongs to: the explicit
// argument first, then the header every agent spawn carries. Shared by the
// checklist store and the goal latch so the two can never disagree about
// which session they are writing to.
func resolveTodoSession(r *http.Request, arg string) string {
	if sid := strings.TrimSpace(arg); sid != "" {
		return sid
	}
	if r != nil {
		return strings.TrimSpace(r.Header.Get("X-Wick-Session-Id"))
	}
	return ""
}
