package slackremote

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yogasw/wick/internal/agents/remote"
)

// MarkerPrefix opens the line a remote ends its reply with.
const MarkerPrefix = "END RESPONSE "

// MarkerInstruction is appended to every turn when the marker is on.
func MarkerInstruction(token string) string {
	return "When your reply is complete, end it with this exact line on its own: " + MarkerPrefix + token
}

// tracker follows one turn: the replies seen so far, keyed by ts, and what
// was already reported, so push events and pull pages can feed it in any
// order and each change is reported once.
type tracker struct {
	mu       sync.Mutex
	channel  string
	threadTS string
	sentTS   string
	token    string // "" = marker off
	// topLevel: a DM, where a bot may answer outside the thread.
	topLevel bool
	ignore   map[string]bool // wick's own user and bot ids
	accept   func(Message) bool
	msgs     map[string]string
	lastTS   string
	shown    string
	status   string
	done     bool
	events   chan remote.Event
}

func newTracker(channel, threadTS, sentTS, token string, topLevel bool, ignore map[string]bool, accept func(Message) bool) *tracker {
	return &tracker{
		channel: channel, threadTS: threadTS, sentTS: sentTS, token: token, topLevel: topLevel,
		ignore: ignore, accept: accept, msgs: map[string]string{}, events: make(chan remote.Event, 256),
	}
}

// tsLess orders Slack timestamps ("1700000000.000100").
func tsLess(a, b string) bool {
	ai, af, _ := strings.Cut(a, ".")
	bi, bf, _ := strings.Cut(b, ".")
	if ai != bi {
		x, _ := strconv.ParseInt(ai, 10, 64)
		y, _ := strconv.ParseInt(bi, 10, 64)
		return x < y
	}
	return af < bf
}

func (t *tracker) belongs(m Message) bool {
	if m.Channel != t.channel {
		return false
	}
	if m.ThreadTS == t.threadTS || m.TS == t.threadTS {
		return true
	}
	return t.topLevel && (m.ThreadTS == "" || m.ThreadTS == m.TS)
}

// observe takes one message and returns the events it causes.
func (t *tracker) observe(m Message) []remote.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done || !t.belongs(m) {
		return nil
	}
	var out []remote.Event
	// Our own message: its reactions say the remote saw it (👀) or works (⏳).
	if m.TS == t.sentTS {
		for _, r := range m.Reactions {
			switch {
			case r == "eyes":
				out = t.setStatus(out, remote.StatusThinking, "")
			case strings.HasPrefix(r, "hourglass"):
				out = t.setStatus(out, remote.StatusWorking, "")
			}
		}
		return out
	}
	if !tsLess(t.sentTS, m.TS) || t.ignore[m.User] || t.ignore[m.BotID] || !t.accept(m) {
		return nil
	}
	if tsLess(t.lastTS, m.TS) {
		t.lastTS = m.TS
	}
	finished := false
	switch strings.ToLower(m.Status) {
	case "thinking":
		out = t.setStatus(out, remote.StatusThinking, "")
	case "working", "tool":
		out = t.setStatus(out, remote.StatusWorking, "")
	case "done", "completed", "complete":
		finished = true
	}
	text := strings.TrimSpace(m.Text)
	if label, ok := progressLine(text); ok {
		delete(t.msgs, m.TS)
		out = t.setStatus(out, remote.StatusWorking, label)
	} else if text != "" {
		t.msgs[m.TS] = text
	}
	visible, marked := t.compose()
	if visible != t.shown {
		t.shown = visible
		out = append(out, remote.Event{Kind: remote.EventText, Text: visible})
	}
	if marked || (finished && visible != "") {
		t.done = true
		out = append(out, remote.Event{Kind: remote.EventDone, Text: visible})
	}
	return out
}

func (t *tracker) setStatus(out []remote.Event, status, detail string) []remote.Event {
	if t.status == status+detail || t.shown != "" && detail == "" {
		return out
	}
	t.status = status + detail
	return append(out, remote.Event{Kind: remote.EventStatus, Status: status, Detail: detail})
}

// compose joins the replies in order and handles the marker: a final line
// carrying the turn's token ends the turn and is dropped; a final line
// that may still grow into it is held back.
func (t *tracker) compose() (string, bool) {
	keys := make([]string, 0, len(t.msgs))
	for k := range t.msgs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return tsLess(keys[i], keys[j]) })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, t.msgs[k])
	}
	text := strings.Join(parts, "\n\n")
	if t.token == "" {
		return text, false
	}
	marker := MarkerPrefix + t.token
	head, last := text, ""
	if i := strings.LastIndex(text, "\n"); i >= 0 {
		head, last = text[:i], text[i+1:]
	} else {
		head, last = "", text
	}
	bare := strings.Trim(strings.TrimSpace(last), "*_`~")
	switch {
	case bare == marker:
		return strings.TrimRight(head, " \n"), true
	case bare != "" && strings.HasPrefix(marker, bare) && len(bare) >= 3:
		return strings.TrimRight(head, " \n"), false
	}
	return text, false
}

// progressLine reads a wick-style progress footer ("Thinking…",
// "_Bash: ls_", ":hourglass: Working") that a message is made of alone.
func progressLine(text string) (string, bool) {
	if text == "" || strings.Contains(text, "\n") || len(text) > 120 {
		return "", false
	}
	s := strings.Trim(text, "_*` ")
	for _, p := range []string{":hourglass_flowing_sand:", ":hourglass:", "⏳", ":eyes:", "👀"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	low := strings.ToLower(strings.TrimRight(s, ".…"))
	if low == "thinking" || low == "working" {
		return "", true
	}
	if tool, rest, ok := strings.Cut(s, ": "); ok && rest != "" {
		switch tool {
		case "Bash", "Read", "Edit", "Write", "Grep", "Glob", "WebFetch", "WebSearch", "Task", "Tool":
			return s, true
		}
	}
	return "", false
}
