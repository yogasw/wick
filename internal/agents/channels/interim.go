package channels

import (
	"regexp"
	"slices"
	"strings"
)

// The in-between text of a Team lead (P60). After team_message returns,
// the model often writes a short line of its own ("Asked Franky and
// Atraxa, both still checking…") that goes stale once they reply. The
// web keeps it in the turn's trace; a chat (Slack, Telegram) holds the
// text of a turn that delegated until the turn ends and drops it while a
// teammate of that turn still works. The same rules as the web's
// isInterimText (fe/agents/conversation/src/lib/teamResults.ts).

// InterimMax is the length past which the text may carry a real answer.
const InterimMax = 600

var (
	// interimTeamTool is team_message under any namespace
	// (mcp__wick__team_message, wick.team_message).
	interimTeamTool = regexp.MustCompile(`(^|__|\.)team_message$`)
	// interimNeutralTool is bookkeeping that does no work of its own:
	// it neither starts nor cancels the hold.
	interimNeutralTool = regexp.MustCompile(`(^|__|\.)(ToolSearch|todo|TodoWrite|wick_set_title|set_title)$`)
	// interimAnswer is a team_message input that answers a task rather
	// than sending a new one.
	interimAnswer = regexp.MustCompile(`"task_id"\s*:\s*"[^"\s]`)
	// interimTaskID reads a task id out of a team_message result (the
	// Hub's JSON, maybe inside an MCP text envelope, quotes escaped).
	interimTaskID = regexp.MustCompile(`"task_id\\?"\s*:\s*\\?"([^"\\]+)`)
	interimFence  = regexp.MustCompile("(?m)^\\s*(```|~~~)")
	interimRow    = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	interimRule   = regexp.MustCompile(`^\s*\|?\s*:?-{3,}`)
)

// HideInterimFn reports whether a session's agent keeps its in-between
// text out of channels ("Hide in-between messages in channels"). Read
// at most once per call site per event, cached by the server: never a
// database read under a channel lock.
type HideInterimFn func(sessionID string) bool

// TasksActiveFn reports whether any of the task ids is still open
// (working, or asking its sender a question).
type TasksActiveFn func(taskIDs []string) bool

// HideInterim and TasksActive are wired by the server at boot; nil
// leaves every text as it was (the hold never starts).
var (
	HideInterim HideInterimFn
	TasksActive TasksActiveFn
)

// HidesInterim is the session's switch, false without a resolver.
func HidesInterim(sessionID string) bool {
	return HideInterim != nil && HideInterim(sessionID)
}

// IsTeamMessageTool reports a team_message call, any namespace.
func IsTeamMessageTool(name string) bool { return interimTeamTool.MatchString(name) }

// IsNeutralTool reports a bookkeeping call (ToolSearch, todo, TodoWrite,
// wick_set_title / set_title), any namespace: it does not count as work.
func IsNeutralTool(name string) bool { return interimNeutralTool.MatchString(name) }

// IsTeamSend reports a team_message call that sends a new task: an
// answer to a task (task_id set) is a reply, not a delegation.
func IsTeamSend(name, input string) bool {
	return IsTeamMessageTool(name) && !interimAnswer.MatchString(input)
}

// IsInterimText applies the tool rule and guard 1: the turn's tool calls
// are team_message only (at least one), and its text asks nothing (no
// line with "?"), has no code fence or markdown table, and is short.
// Neutral calls (IsNeutralTool) are skipped.
func IsInterimText(toolNames []string, text string) bool {
	team := false
	for _, n := range toolNames {
		if IsNeutralTool(n) {
			continue
		}
		if !IsTeamMessageTool(n) {
			return false
		}
		team = true
	}
	if !team {
		return false
	}
	body := strings.TrimSpace(text)
	if body == "" || len(body) > InterimMax {
		return false
	}
	lines := strings.Split(body, "\n")
	row, rule := false, false
	for _, l := range lines {
		if strings.Contains(l, "?") {
			return false
		}
		row = row || interimRow.MatchString(l)
		rule = rule || interimRule.MatchString(l)
	}
	if row && rule {
		return false
	}
	return !interimFence.MatchString(body)
}

// InterimTurn follows one turn's tool calls for the hold. The zero
// value is a turn that has called nothing. Not safe for concurrent use:
// the channel keeps it on its turn, under its own lock.
type InterimTurn struct {
	held  bool            // a team_message send asked for the hold (switch on)
	other bool            // the turn did other work: never held
	names []string        // the team_message sends
	uses  map[string]bool // their tool_use ids, to pair the results
	tasks []string        // the task ids those results name
	cut   int             // text length at the last team_message result
}

// Tool records a ToolUse. hide is the session's switch, read by the
// caller outside its lock (only needed when IsTeamSend). A neutral call
// changes nothing.
func (it *InterimTurn) Tool(name, input, useID string, hide bool) {
	if IsNeutralTool(name) {
		return
	}
	if !IsTeamSend(name, input) {
		it.other = true
		return
	}
	it.names = append(it.names, name)
	if hide {
		it.held = true
	}
	if useID != "" {
		if it.uses == nil {
			it.uses = map[string]bool{}
		}
		it.uses[useID] = true
	}
}

// Result records a ToolResult: a team_message result names its task.
// at is the length of the turn's text so far: what comes after the last
// team_message result is the answer, never in-between text.
func (it *InterimTurn) Result(name, useID, text string, at int) {
	if !it.uses[useID] && !IsTeamMessageTool(name) {
		return
	}
	it.cut = at
	for _, m := range interimTaskID.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(it.tasks, m[1]) {
			it.tasks = append(it.tasks, m[1])
		}
	}
}

// Holding reports whether the turn's text waits for the turn end: it
// sent through team_message, the switch is on, and it did nothing else.
func (it *InterimTurn) Holding() bool { return it.held && !it.other }

// Final decides at turn end, outside the channel lock, and returns the
// text to post. Only the text up to the last team_message result is
// in-between text; what streamed after it is the answer and posts as
// usual. That part is dropped only when it is in-between text
// (IsInterimText) and a task the turn sent is still open (guard 2). All
// settled, or no task found → the whole text posts, so the lead is never
// silent. dropped reports that the part was left out.
func (it *InterimTurn) Final(text string) (post string, dropped bool) {
	if !it.Holding() || len(it.tasks) == 0 || TasksActive == nil {
		return text, false
	}
	cut := min(max(it.cut, 0), len(text))
	if !IsInterimText(it.names, text[:cut]) || !TasksActive(it.tasks) {
		return text, false
	}
	return strings.TrimSpace(text[cut:]), true
}

// Head is the turn's text up to the last team_message result, trimmed:
// the part Final leaves out.
func (it *InterimTurn) Head(text string) string {
	return strings.TrimSpace(text[:min(max(it.cut, 0), len(text))])
}
