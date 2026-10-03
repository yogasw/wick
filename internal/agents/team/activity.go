package team

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
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

// TurnStatus is the roster status of a session: "running" only while a
// turn is in flight. metaStatus is the persisted session status, which
// the pool sets to running at spawn and back to idle only when the
// process exits — a warm process between turns still reads "running"
// there. lifecycle is the pool's live view ("" when no process).
func TurnStatus(metaStatus, lifecycle string) string {
	switch lifecycle {
	case "working", "spawning":
		return "running"
	}
	if lifecycle == "" && metaStatus == "queued" {
		return "queued"
	}
	return "idle"
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

// maxPreviewRunes bounds a roster preview to one line of the row.
const maxPreviewRunes = 80

// previewTailBytes is how much of conversation.jsonl a preview reads:
// enough for the last turn or two, never the whole transcript.
const previewTailBytes = 64 << 10

// AskPreview is the roster preview while an ask_user question waits.
func AskPreview(question string) string {
	return PreviewText("Butuh input: " + question)
}

// ApprovalPreview is the roster preview while a tool approval waits.
func ApprovalPreview(tool string) string {
	if strings.TrimSpace(tool) == "" {
		tool = "Aksi"
	}
	return PreviewText(ActionLabel(tool, "") + " — butuh approval")
}

// PreviewText flattens a message to one plain line: markdown markers
// and links collapse to their text, whitespace to single spaces, and the
// result is clipped to maxPreviewRunes.
func PreviewText(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdFence.ReplaceAllString(s, " ")
	s = mdLinePrefix.ReplaceAllString(s, "")
	s = strings.NewReplacer("**", "", "__", "", "`", "", "~~", "").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxPreviewRunes {
		s = strings.TrimSpace(string(r[:maxPreviewRunes-1])) + "…"
	}
	return s
}

var (
	mdLink       = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdFence      = regexp.MustCompile("(?m)^```.*$")
	mdLinePrefix = regexp.MustCompile(`(?m)^\s*(#{1,6}\s+|>\s?|[-*+]\s+|\d+\.\s+)`)
)

// TailPreview returns the preview of the newest user or assistant turn
// in a conversation.jsonl, reading only its last previewTailBytes. ""
// when the file is missing or the tail holds no turn with text.
func TailPreview(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := st.Size() - previewTailBytes
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return ""
	}
	lines := bytes.Split(buf, []byte{'\n'})
	// A cut tail starts mid-line; that fragment never parses, so it is
	// skipped like any other bad line.
	for i := len(lines) - 1; i >= 0; i-- {
		var t store.ConversationTurn
		if json.Unmarshal(lines[i], &t) != nil {
			continue
		}
		if t.Role != "user" && t.Role != "assistant" {
			continue
		}
		if p := PreviewText(t.Text); p != "" {
			return p
		}
	}
	return ""
}
