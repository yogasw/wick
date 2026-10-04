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

// AllowsProviderSwitch reads AllowProviderSwitch with its default: the
// Captain may switch provider in chat, every other agent stays on the one
// its settings name, unless the owner said otherwise.
func AllowsProviderSwitch(set *bool, isCaptain bool) bool {
	if set != nil {
		return *set
	}
	return isCaptain
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
	return PreviewText("Needs input: " + question)
}

// ApprovalPreview is the roster preview while a tool approval waits.
func ApprovalPreview(tool string) string {
	if strings.TrimSpace(tool) == "" {
		tool = "Action"
	}
	return PreviewText(ActionLabel(tool, "") + " — needs approval")
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

// SourceTeam is the Source of a user turn wick posted on a teammate's
// behalf: another agent asked (team_message, an @mention, A2A).
const SourceTeam = "team"

// unreadTailBytes is how much of conversation.jsonl UnreadCount reads.
const unreadTailBytes = 512 << 10

// UndeliveredAfter is how long an answer to a teammate may wait for its
// task to be marked done before it counts as reaching no one.
const UndeliveredAfter = time.Minute

// UnreadCount counts the replies of a main session the owner has not
// read: every reply after lastRead to a person (web chat, a Slack
// mention, a schedule), but a reply to a teammate only when it reached
// no one — its task failed, was canceled, or never closed within
// UndeliveredAfter. A delivered one is the asker's to report, so it is
// read here. settled is false while an answer still waits on its task:
// the count may change with no new line in the file.
func UnreadCount(path string, lastRead *time.Time, now time.Time) (n int, settled bool) {
	buf := readTail(path, unreadTailBytes)
	var (
		lastWorking string      // task of the latest working handoff
		asked       bool        // the running turn came from a teammate
		askTask     string      // that teammate's task, "" when unknown
		waiting     []time.Time // replies to it, not yet delivered
	)
	resolve := func(delivered bool) {
		if !delivered {
			n += len(waiting)
		}
		waiting = nil
	}
	for _, line := range bytes.Split(buf, []byte{'\n'}) {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			continue
		}
		fresh := lastRead == nil || t.Timestamp.After(*lastRead)
		switch {
		case t.Role == "user":
			// A new message while an answer still waits: that task
			// never closed, so the answer reached no one.
			resolve(false)
			asked, askTask = t.Source == SourceTeam, ""
			if asked {
				askTask = lastWorking
			}
		case t.Role == "system" && t.Kind == store.KindMentionHandoff:
			state := strings.ToLower(t.Extras["state"])
			task := t.Extras["task_id"]
			if strings.Contains(state, "working") || strings.Contains(state, "submitted") {
				lastWorking = task
				continue
			}
			if asked && len(waiting) > 0 && (askTask == "" || task == askTask) {
				resolve(strings.Contains(state, "completed"))
			}
		case t.Role == "assistant" || t.Role == "system" && t.IsError:
			if !fresh {
				continue
			}
			if asked {
				waiting = append(waiting, t.Timestamp)
			} else {
				n++
			}
		}
	}
	if len(waiting) > 0 {
		if now.Sub(waiting[len(waiting)-1]) < UndeliveredAfter {
			return n, false
		}
		n += len(waiting)
	}
	return n, true
}

// readTail returns up to the last max bytes of path; a cut first line
// never parses, so callers skip it like any other bad line.
func readTail(path string, max int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	off := st.Size() - max
	if off < 0 {
		off = 0
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil
	}
	return buf
}
