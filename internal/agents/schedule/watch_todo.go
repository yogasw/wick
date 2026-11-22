package schedule

import (
	"fmt"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// Extract keys a tracked watch's check step fills (see the connector Track
// follow-up): the job state and an optional detail line.
const (
	WatchTodoStateKey  = "state"
	WatchTodoDetailKey = "detail"
)

// watchTodoDetailTail caps the detail shown under the checklist item: the
// end of a log is the part worth reading.
const watchTodoDetailTail = 1500

// syncWatchTodo mirrors one run of a watch into its checklist item, when
// the watch carries one. terminal is the status the run ended the watch
// with ("" = still running).
func (r *Runner) syncWatchTodo(l zerologLogger, m entity.ScheduledMessage, rec RunRecord, terminal string) {
	t := WatchTodoOf(m.Steps)
	if t == nil || m.SessionID == "" || r.layout == (agentconfig.Layout{}) {
		return
	}
	status, body := watchTodoPatch(m, rec, terminal, time.Now())
	if _, err := session.PatchTodoItem(r.layout, m.SessionID, session.TodoPatch{
		Select: t.Item,
		Title:  t.Title,
		Status: status,
		Detail: &session.TodoDetail{Format: "text", Body: body},
	}); err != nil {
		l.Warn().Str("id", m.ID).Err(err).Msg("watch: checklist update failed")
		return
	}
	if r.todoNotify != nil {
		r.todoNotify(m.SessionID)
	}
}

// watchTodoPatch is the item status and detail text for one run. Pure, so
// the mapping is testable without a runner.
func watchTodoPatch(m entity.ScheduledMessage, rec RunRecord, terminal string, now time.Time) (string, string) {
	status := "in_progress"
	switch terminal {
	case "":
	case entity.ScheduledStatusDone:
		if rec.Result == entity.WatchResultMatched && rec.Outcome != OutcomeFail {
			status = "completed"
		} else {
			status = "failed"
		}
	default:
		status = "failed"
	}
	var b strings.Builder
	state := extractString(rec.Extract, WatchTodoStateKey)
	if state == "" {
		state = "-"
	}
	fmt.Fprintf(&b, "state: %s\n", state)
	if d := extractString(rec.Extract, WatchTodoDetailKey); d != "" {
		if r := []rune(d); len(r) > watchTodoDetailTail {
			d = "…" + string(r[len(r)-watchTodoDetailTail:])
		}
		b.WriteString(d)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "check #%d at %s", rec.Run, now.Format("15:04:05"))
	switch {
	case status == "in_progress" && rec.Result == entity.WatchResultError:
		fmt.Fprintf(&b, " (error, retrying: %s)", clip(Redact(rec.Error)))
	case status == "failed" && rec.Reason != "":
		fmt.Fprintf(&b, "\n%s", clip(Redact(rec.Reason)))
	}
	fmt.Fprintf(&b, "\nwatch %s", m.ID)
	return status, b.String()
}

func extractString(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(v))
}
