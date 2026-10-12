package agents

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/yogasw/wick/internal/agents/teamlink"
)

// A turn's trace is the provider's events as stored at commit; a hand-off
// to a teammate lives on in the Hub (the task and its thread), so the
// trace endpoint merges it in at read time instead of writing it into
// trace storage. Each task the turn sent gets one team_task event after
// the events, tied to the team_message call that sent it (tool_use_id);
// an @handle line in the turn's prose sent its task with no call at all,
// so its team_task is the synthetic event, marked via "mention".

// teamTraceEntries is the most thread entries a team_task event carries
// (the newest ones), and teamTraceText the most characters of each.
const (
	teamTraceEntries = 20
	teamTraceText    = 600
	// teamTraceSlack widens the turn's window for a mention task: the
	// router sends it as the turn's text lands, around its last event.
	teamTraceSlack = 2 * time.Second
)

var (
	// teamTraceTaskID reads a task id out of a team_message result (the
	// Hub's JSON, sometimes inside an MCP text envelope, so the quotes
	// may be escaped) — the same rule as the web's teamTaskIdsFromEvents.
	teamTraceTaskID = regexp.MustCompile(`"task_id\\?"\s*:\s*\\?"([^"\\]+)`)
	// teamTraceAnswer is a team_message input that answers a task.
	teamTraceAnswer = regexp.MustCompile(`"task_id"\s*:\s*"[^"\s]`)
	teamTraceTool   = regexp.MustCompile(`(^|__)team_message$`)
)

// teamTraceEvent is one event of the trace index, as the web reads it.
type teamTraceEvent struct {
	EventID   string    `json:"event_id,omitempty"`
	Type      string    `json:"type"`
	ToolName  string    `json:"tool_name,omitempty"`
	ToolUseID string    `json:"tool_use_id,omitempty"`
	At        time.Time `json:"at,omitzero"`
	EndAt     time.Time `json:"end_at,omitzero"`
	Text      string    `json:"text,omitempty"`
	ToolInput string    `json:"tool_input,omitempty"`
}

// teamTraceTask is a team_task event's text: the task and its thread.
type teamTraceTask struct {
	TaskID   string                 `json:"task_id"`
	Via      string                 `json:"via"`
	ToHandle string                 `json:"to_handle"`
	ToName   string                 `json:"to_name,omitempty"`
	Title    string                 `json:"title,omitempty"`
	State    string                 `json:"state"`
	Started  time.Time              `json:"started_at,omitzero"`
	Ended    time.Time              `json:"ended_at,omitzero"`
	Entries  []teamlink.ThreadEntry `json:"entries,omitempty"`
}

// mergeTeamTrace appends the turn's hand-offs to a stored trace index:
// sent is every task the session sent, thread reads one task's thread.
// The trace comes back unchanged when it does not parse or the turn
// handed nothing off.
func mergeTeamTrace(data []byte, sent []teamlink.TaskView, thread func(taskID string) []teamlink.ThreadEntry) []byte {
	if len(sent) == 0 {
		return data
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		return data
	}
	// The stored rows stay as they are (every field the web reads); only
	// the few this needs are decoded.
	var rows []json.RawMessage
	if raw, ok := root["events"]; !ok || json.Unmarshal(raw, &rows) != nil {
		return data
	}
	events := make([]teamTraceEvent, len(rows))
	for i, r := range rows {
		_ = json.Unmarshal(r, &events[i])
	}
	byID := make(map[string]teamlink.TaskView, len(sent))
	for _, t := range sent {
		byID[t.TaskID] = t
	}

	// The tasks the turn's team_message calls sent, in call order.
	uses := map[string]bool{}
	answers := map[string]bool{}
	for _, e := range events {
		if e.Type == "tool_use" && teamTraceTool.MatchString(e.ToolName) {
			uses[e.ToolUseID] = true
			if teamTraceAnswer.MatchString(e.ToolInput) {
				answers[e.ToolUseID] = true
			}
		}
	}
	type sentBy struct{ id, useID string }
	var order []sentBy
	claimed := map[string]bool{}
	var start, end time.Time
	for _, e := range events {
		for _, at := range []time.Time{e.At, e.EndAt} {
			if at.IsZero() {
				continue
			}
			if start.IsZero() || at.Before(start) {
				start = at
			}
			if at.After(end) {
				end = at
			}
		}
		if e.ToolUseID != "" && answers[e.ToolUseID] {
			continue
		}
		isUse := e.Type == "tool_use" && teamTraceTool.MatchString(e.ToolName)
		isResult := e.Type == "tool_result" && (uses[e.ToolUseID] || teamTraceTool.MatchString(e.ToolName))
		if !isUse && !isResult {
			continue
		}
		m := teamTraceTaskID.FindStringSubmatch(e.Text)
		if m == nil || claimed[m[1]] {
			continue
		}
		claimed[m[1]] = true
		order = append(order, sentBy{m[1], e.ToolUseID})
	}

	var add []teamTraceEvent
	for _, s := range order {
		if t, ok := byID[s.id]; ok {
			add = append(add, teamTaskEvent(t, "tool", s.useID, thread))
		}
	}
	// An @handle line: the agent's own task, started inside the turn,
	// that no call of the turn sent — its synthetic event is the
	// team_task with via "mention". The person's own tasks and a Retry or
	// Reassign they pressed (retry_of) are not the agent's.
	if !start.IsZero() {
		for _, t := range sent {
			if claimed[t.TaskID] || t.Origin == "user" || t.RetryOf != "" || t.Started.IsZero() {
				continue
			}
			if t.Started.Before(start.Add(-teamTraceSlack)) || t.Started.After(end.Add(teamTraceSlack)) {
				continue
			}
			add = append(add, teamTaskEvent(t, "mention", "", thread))
		}
	}
	if len(add) == 0 {
		return data
	}
	for _, e := range add {
		b, err := json.Marshal(e)
		if err != nil {
			return data
		}
		rows = append(rows, b)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return data
	}
	root["events"] = raw
	out, err := json.Marshal(root)
	if err != nil {
		return data
	}
	return out
}

// teamTaskEvent is one task as a team_task event, its newest thread
// entries capped.
func teamTaskEvent(t teamlink.TaskView, via, useID string, thread func(string) []teamlink.ThreadEntry) teamTraceEvent {
	tt := teamTraceTask{TaskID: t.TaskID, Via: via, ToHandle: t.ToHandle, ToName: t.ToName, Title: t.Title, State: t.State, Started: t.Started, Ended: t.EndedAt}
	if thread != nil {
		entries := thread(t.TaskID)
		if len(entries) > teamTraceEntries {
			entries = entries[len(entries)-teamTraceEntries:]
		}
		for _, e := range entries {
			if r := []rune(e.Text); len(r) > teamTraceText {
				e.Text = string(r[:teamTraceText-1]) + "…"
			}
			tt.Entries = append(tt.Entries, e)
		}
	}
	b, _ := json.Marshal(tt)
	at := t.EndedAt
	if at.IsZero() {
		at = t.Updated
	}
	return teamTraceEvent{Type: "team_task", ToolUseID: useID, At: t.Started, EndAt: at, Text: string(b)}
}
