package team

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// maxActionRunes bounds CurrentAction so a roster row never wraps.
const maxActionRunes = 48

// Unread reports whether the main session moved after the owner last
// opened the chat. A session with no activity is never unread; one the
// owner has never opened is, once it has any.
func Unread(lastActive time.Time, lastRead *time.Time) bool {
	if lastActive.IsZero() {
		return false
	}
	return lastRead == nil || lastActive.After(*lastRead)
}

// CurrentAction names the tool the in-flight turn is waiting on: the
// newest tool_use with no tool_result after it. "" when the turn is
// thinking or writing, or no turn is running.
func CurrentAction(evs []store.TurnEvent) string {
	done := map[string]bool{}
	for i := len(evs) - 1; i >= 0; i-- {
		e := evs[i]
		switch e.Type {
		case "tool_result":
			if e.ToolUseID != "" {
				done[e.ToolUseID] = true
			}
		case "tool_use":
			if e.ToolUseID != "" && done[e.ToolUseID] {
				return ""
			}
			return ActionLabel(e.ToolName, e.ToolInput)
		}
	}
	return ""
}

// ActionLabel shortens a tool call to what a person reads in the roster:
// an MCP tool loses its "mcp__<server>__" prefix, and a wick_execute call
// shows the connector op it runs ("query_range") rather than the
// dispatcher's own name.
func ActionLabel(name, input string) string {
	if strings.HasPrefix(name, "mcp__") {
		if i := strings.LastIndex(name, "__"); i > len("mcp_") {
			name = name[i+2:]
		}
	}
	if name == "wick_execute" {
		var in struct {
			ToolID string `json:"tool_id"`
		}
		if json.Unmarshal([]byte(input), &in) == nil {
			if op := opOfToolID(in.ToolID); op != "" {
				name = op
			}
		}
	}
	if r := []rune(name); len(r) > maxActionRunes {
		name = string(r[:maxActionRunes-1]) + "…"
	}
	return name
}

// opOfToolID takes the op out of "conn:<id>/<op>[@<account>]".
func opOfToolID(id string) string {
	i := strings.LastIndex(id, "/")
	if i < 0 {
		return ""
	}
	op := id[i+1:]
	if j := strings.Index(op, "@"); j >= 0 {
		op = op[:j]
	}
	return op
}
