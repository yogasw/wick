package provider

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/store"
)

// failover_continue.go builds the message a failover sends the target so
// the turn carries on: the user's message again when nothing came out
// yet, otherwise a "[wick] continue" note listing what already ran.
// Deterministic text cutting only — no model is asked to summarize.

// Continue message caps.
const (
	// ContinueTailMax is how much of the turn's text a cross-type
	// continue quotes (its tail, where the turn stopped).
	ContinueTailMax = 1500
	// ContinueToolsMax is how many tools a continue lists.
	ContinueToolsMax = 10
	// ContinueToolInputMax caps one tool's input in the list.
	ContinueToolInputMax = 80
	// ContinueMax caps the whole continue message.
	ContinueMax = 4000
)

// Tool statuses in a ToolBrief.
const (
	ToolOK      = "ok"
	ToolError   = "error"
	ToolUnknown = "unknown"
)

// ToolBrief is one tool the failed turn ran.
type ToolBrief struct {
	Name string
	// Input is the tool input on one line, cut to ContinueToolInputMax.
	Input string
	// Status is ToolOK, ToolError, or ToolUnknown for a tool still in
	// flight when the provider failed — it may or may not have run.
	Status string
}

// TurnSnapshot is the failed turn's output, taken before the error is
// applied to the store (which drops the in-flight events). Text holds the
// turn's NON-synthetic text only: a provider's own limit notice is
// neither output nor worth quoting.
type TurnSnapshot struct {
	UserMsg  string
	Text     string
	Tools    []ToolBrief
	Produced bool
}

// NewTurnSnapshot builds a snapshot from a turn's user message, its
// non-synthetic text and its in-flight events. Produced = any text or any
// tool.
func NewTurnSnapshot(userMsg, text string, events []store.TurnEvent) TurnSnapshot {
	tools := briefTools(events)
	return TurnSnapshot{
		UserMsg:  userMsg,
		Text:     text,
		Tools:    tools,
		Produced: strings.TrimSpace(text) != "" || len(tools) > 0,
	}
}

// Combine is s followed by next, for a second hop: the continue then
// covers the output of both providers. The user message is always s's —
// the one the run is for; next's is the continue text the second
// provider was sent, and quoting it would nest one continue in another.
func (s TurnSnapshot) Combine(next TurnSnapshot) TurnSnapshot {
	out := s
	switch {
	case strings.TrimSpace(s.Text) == "":
		out.Text = next.Text
	case strings.TrimSpace(next.Text) != "":
		out.Text = s.Text + "\n\n" + next.Text
	}
	out.Tools = append(append([]ToolBrief(nil), s.Tools...), next.Tools...)
	out.Produced = s.Produced || next.Produced
	return out
}

// briefTools lists the tool_use events of a turn with the outcome of
// their tool_result. A tool_use with no result is in flight: ToolUnknown.
func briefTools(events []store.TurnEvent) []ToolBrief {
	var out []ToolBrief
	byID := map[string]int{}
	for _, ev := range events {
		switch ev.Type {
		case "tool_use":
			if ev.ToolUseID != "" {
				byID[ev.ToolUseID] = len(out)
			}
			out = append(out, ToolBrief{Name: ev.ToolName, Input: oneLine(ev.ToolInput, ContinueToolInputMax), Status: ToolUnknown})
		case "tool_result":
			i, ok := byID[ev.ToolUseID]
			if !ok {
				// No id to match: the latest tool still waiting.
				i = -1
				for j := len(out) - 1; j >= 0; j-- {
					if out[j].Status == ToolUnknown {
						i = j
						break
					}
				}
				if i < 0 {
					continue
				}
			}
			out[i].Status = ToolOK
			if ev.IsError {
				out[i].Status = ToolError
			}
		}
	}
	return out
}

// ContinueInput is what ContinueMessage needs.
type ContinueInput struct {
	// From / To are the "type/name" keys the turn moves between.
	From, To string
	Reason   event.ErrKind
	ResetAt  time.Time
	// HistoryCarries: the target resumes this conversation
	// (SwitchResult.HistoryCarries), so it needs no recap. Sharing a
	// provider type is not enough — a same-type target that cannot reach
	// the transcript starts fresh.
	HistoryCarries bool
	UserMsg        string
	Produced       bool
	PartialText    string
	Tools          []ToolBrief
}

// ContinueInputFrom fills a ContinueInput from the switch that moved the
// turn and the turn's snapshot.
func ContinueInputFrom(res SwitchResult, reason event.ErrKind, resetAt time.Time, snap TurnSnapshot) ContinueInput {
	return ContinueInput{
		From: res.From, To: res.To, Reason: reason, ResetAt: resetAt,
		HistoryCarries: res.HistoryCarries,
		UserMsg:        snap.UserMsg,
		Produced:       snap.Produced,
		PartialText:    snap.Text,
		Tools:          snap.Tools,
	}
}

// ContinueMessage is the message the failover target receives:
//   - nothing produced yet: the user's message, unchanged;
//   - produced, history carries over: a [wick] note to continue without
//     repeating the tools that ran;
//   - produced, history does not carry (another type, or a same-type
//     target starting fresh): the same note plus the user's message and
//     the tail of the text already produced.
//
// Capped at ContinueMax characters; the user's message is cut first.
func ContinueMessage(in ContinueInput) string {
	if !in.Produced {
		return in.UserMsg
	}
	var head strings.Builder
	head.WriteString("[wick] The previous provider (" + in.From + ") stopped mid-turn: " + FailoverReason(in.Reason, in.ResetAt) + ". ")
	if in.HistoryCarries {
		head.WriteString("You are now on " + in.To + " with the same conversation. ")
	} else {
		head.WriteString("You are now on " + in.To + ". Your conversation history did not carry over. ")
	}
	head.WriteString("Continue from where it stopped. Do NOT repeat actions that already ran (tools listed below).")

	tools := toolsSection(in.Tools)
	if in.HistoryCarries {
		return capRunes(head.String()+"\n\n"+tools, ContinueMax)
	}

	summary := ""
	if tail := summarizeTail(in.PartialText, ContinueTailMax); tail != "" {
		summary = "\n\nOutput already produced this turn (summary): \"\"\"" + tail + "\"\"\""
	}
	const userOpen, userClose = "\n\nLast user message: \"\"\"", "\"\"\""
	fixed := runeLen(head.String()) + runeLen(userOpen) + runeLen(userClose) + runeLen(summary) + 2 + runeLen(tools)
	user := capRunes(in.UserMsg, max(ContinueMax-fixed, 0))
	return capRunes(head.String()+userOpen+user+userClose+summary+"\n\n"+tools, ContinueMax)
}

// toolsSection lists tools (the last ContinueToolsMax, the most recent
// being where the turn stopped).
func toolsSection(tools []ToolBrief) string {
	if len(tools) == 0 {
		return "Tools already run: none."
	}
	var b strings.Builder
	b.WriteString("Tools already run:")
	if n := len(tools) - ContinueToolsMax; n > 0 {
		b.WriteString("\n- (" + strconv.Itoa(n) + " earlier tools omitted)")
		tools = tools[n:]
	}
	for _, t := range tools {
		status := t.Status
		if status == ToolUnknown || status == "" {
			status = "status unknown — verify before repeating"
		}
		b.WriteString("\n- " + t.Name + "(" + t.Input + ") → " + status)
	}
	return b.String()
}

// summarizeTail is the last max characters of text, trimmed, led by "…"
// when cut. The cut moves forward to a word boundary when one is near, so
// the quote does not open mid-word.
func summarizeTail(text string, max int) string {
	text = strings.TrimSpace(text)
	r := []rune(text)
	if max <= 0 {
		return ""
	}
	if len(r) <= max {
		return text
	}
	r = r[len(r)-(max-1):]
	for i := 0; i < len(r) && i < 40; i++ {
		if unicode.IsSpace(r[i]) {
			r = r[i+1:]
			break
		}
	}
	return "…" + strings.TrimSpace(string(r))
}

// oneLine collapses whitespace and cuts s to max characters.
func oneLine(s string, max int) string {
	return capRunes(strings.Join(strings.Fields(s), " "), max)
}

// capRunes cuts s to max characters, the last one "…" when cut.
func capRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	return string(r[:max-1]) + "…"
}

func runeLen(s string) int { return len([]rune(s)) }
