package ticket

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// ThreadsField is the ticket field listing the chat threads a ticket lives
// in, as "<chat_id>/<thread_id>" tokens separated by commas, spaces or
// newlines (a Slack thread is "C030CBY48KF/1792044355.318329"). Whatever
// opens the ticket (a workflow, a person) writes it; auto-join reads it to
// put a session born in one of those threads on the ticket.
const ThreadsField = "threads"

// Auto-join rules, reported back so the caller can log which one fired.
const (
	JoinByThread    = "thread"    // J1: the session's own thread is listed
	JoinByParent    = "parent"    // J2: a sub-agent follows its parent
	JoinByPermalink = "permalink" // J3: the first message links a listed thread
)

// AutoJoinDeps are what EvaluateAutoJoin needs from outside.
type AutoJoinDeps struct {
	Layout config.Layout
	// LoadConfig returns a project's ticket config. Auto-join only runs
	// when ticket mode is on (Enabled), and reads the statuses to tell a
	// finished ticket from a live one.
	LoadConfig func(projectID string) (project.TicketConfig, error)
	// WriteBackPointer keeps session.Meta.TicketID in step. Optional.
	WriteBackPointer func(sessionID, ticketID string)
}

// AutoJoinInput describes the session at its first user message.
type AutoJoinInput struct {
	// ChatID / ThreadID are the session's channel thread (ChannelRef);
	// empty for a session that is not bound to one.
	ChatID   string
	ThreadID string
	// ParentSessionID is set on a sub-agent's session.
	ParentSessionID string
	// Room is true for a ticket room or a group member's backing session:
	// those belong to their room, not to whatever ticket a thread names.
	Room bool
	// FirstMessage is the opening text, searched for Slack permalinks.
	FirstMessage string
}

// AutoJoinResult says what EvaluateAutoJoin did.
type AutoJoinResult struct {
	// Joined is true when the session was attached to Ticket.
	Joined bool
	Ticket Ticket
	// Rule is the rule that picked Ticket (JoinBy*).
	Rule string
	// Ambiguous lists the candidate ticket ids when more than one matched
	// and nothing was attached. A known thread on two live tickets is a
	// mix-up for a human to sort out, not a guess for the server to make.
	Ambiguous []string
}

// EvaluateAutoJoin puts a session that has no ticket yet on the ticket its
// thread already belongs to, so the ticket's prompt (notes, owner) is there
// from the session's first turn. Deterministic: it matches ids, it does not
// read meaning into the text.
//
// A candidate is a live (non-terminal) ticket of the session's project that
//   - lists the session's own thread in ThreadsField (J1), or
//   - holds the session's parent session (J2), or
//   - lists a thread the first message links to by Slack permalink (J3).
//
// Exactly one candidate is attached; a J3 join also adds the session's own
// thread to the ticket's ThreadsField so the next session born there joins
// by J1. More than one candidate attaches nothing and is reported in
// Ambiguous. Safe to call on every first message.
func EvaluateAutoJoin(d AutoJoinDeps, projectID, sessionID string, in AutoJoinInput) AutoJoinResult {
	if projectID == "" || sessionID == "" || d.LoadConfig == nil || in.Room {
		return AutoJoinResult{}
	}
	if _, already := FindBySession(d.Layout, projectID, sessionID); already {
		return AutoJoinResult{}
	}
	cfg, err := d.LoadConfig(projectID)
	if err != nil || !cfg.Enabled {
		return AutoJoinResult{}
	}
	tickets, err := List(d.Layout, projectID)
	if err != nil {
		return AutoJoinResult{}
	}

	own := threadToken(in.ChatID, in.ThreadID)
	linked := permalinkTokens(in.FirstMessage)
	rules := map[string]string{} // ticket id -> first rule that picked it
	byID := map[string]Ticket{}
	pick := func(tk Ticket, rule string) {
		if _, seen := rules[tk.ID]; !seen {
			rules[tk.ID] = rule
			byID[tk.ID] = tk
		}
	}
	for _, tk := range tickets {
		if isTerminal(cfg, tk.Status) {
			continue
		}
		listed := ThreadTokens(tk.Fields[ThreadsField])
		if own != "" && listed[own] {
			pick(tk, JoinByThread)
		}
		if in.ParentSessionID != "" && hasSession(tk, in.ParentSessionID) {
			pick(tk, JoinByParent)
		}
		for _, l := range linked {
			if listed[l] {
				pick(tk, JoinByPermalink)
				break
			}
		}
	}

	if len(rules) == 0 {
		return AutoJoinResult{}
	}
	if len(rules) > 1 {
		ids := make([]string, 0, len(rules))
		for id := range rules {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return AutoJoinResult{Ambiguous: ids}
	}

	var tk Ticket
	var rule string
	for id, r := range rules {
		tk, rule = byID[id], r
	}
	if err := AttachSession(d.Layout, projectID, tk.ID, sessionID); err != nil {
		return AutoJoinResult{}
	}
	if d.WriteBackPointer != nil {
		d.WriteBackPointer(sessionID, tk.ID)
	}
	if fresh, lerr := Load(d.Layout, projectID, tk.ID); lerr == nil {
		tk = fresh
	}
	// A J3 join teaches the ticket this thread, so the next session born
	// in it joins by J1 without needing the link again.
	if rule == JoinByPermalink && own != "" && !ThreadTokens(tk.Fields[ThreadsField])[own] {
		if tk.Fields == nil {
			tk.Fields = map[string]string{}
		}
		tk.Fields[ThreadsField] = appendThreadToken(tk.Fields[ThreadsField], own)
		if saved, serr := SaveAsAt(d.Layout, tk, Actor{}, time.Time{}); serr == nil {
			tk = saved
		}
	}
	return AutoJoinResult{Joined: true, Ticket: tk, Rule: rule}
}

// threadToken is the ThreadsField token for a chat thread, "" when either
// half is missing.
func threadToken(chatID, threadID string) string {
	chatID, threadID = strings.TrimSpace(chatID), strings.TrimSpace(threadID)
	if chatID == "" || threadID == "" {
		return ""
	}
	return chatID + "/" + threadID
}

// ThreadTokens splits a ThreadsField value into its tokens. Matching is on
// whole tokens: "C1/1.2" does not match a field holding "C1/1.23".
func ThreadTokens(v string) map[string]bool {
	out := map[string]bool{}
	for _, t := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}) {
		out[t] = true
	}
	return out
}

// appendThreadToken adds tok to a ThreadsField value in the comma form the
// field is written in.
func appendThreadToken(v, tok string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return tok
	}
	return v + "," + tok
}

// slackPermalink matches a Slack message permalink: the channel, the
// message ts written as p + 16 digits, and an optional thread_ts query.
var slackPermalink = regexp.MustCompile(
	`https://(?:[A-Za-z0-9-]+\.)*slack\.com/archives/([A-Z0-9]+)/p(\d{10})(\d{6})(?:\?([^\s>|]*))?`)

var threadTSParam = regexp.MustCompile(`(?:^|&)thread_ts=(\d+\.\d+)`)

// permalinkTokens returns the thread tokens the Slack permalinks in text
// point at. A link to a reply carries its thread's root in thread_ts, and
// that root is what a ticket lists, so it comes first; the message's own ts
// is kept too, since a link to the root message has no thread_ts at all.
func permalinkTokens(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(t string) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, m := range slackPermalink.FindAllStringSubmatch(text, -1) {
		if q := threadTSParam.FindStringSubmatch(m[4]); q != nil {
			add(m[1] + "/" + q[1])
		}
		add(m[1] + "/" + m[2] + "." + m[3])
	}
	return out
}

// isTerminal reports whether status is the project's finished stage.
func isTerminal(cfg project.TicketConfig, status string) bool {
	for _, s := range cfg.StatusList() {
		if s.Key == status {
			return s.Terminal
		}
	}
	return false
}

func hasSession(tk Ticket, sessionID string) bool {
	for _, s := range tk.Sessions {
		if s == sessionID {
			return true
		}
	}
	return false
}
