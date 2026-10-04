package slackremote

import (
	"regexp"
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
	// What says the remote still works: ⏳ on our message, the message
	// whose metadata set a status, the newest progress note and its label.
	hourglass bool
	// reopened: the turn ended and a grace window follows it (Reopen).
	reopened   bool
	statusTS   string
	progressTS string
	label      string
	done       bool
	events     chan remote.Event
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
//
// Every answer message is passed on as it comes (EventText, the replies
// joined in order), an edit changes it in place, a deletion drops it.
// The turn ends at once on the marker, a ✅/❌ reaction on our message or
// a done status; otherwise the runner's idle window ends it, which does
// not run while the remote shows it is working: its latest message is a
// progress note, our message carries ⏳, or a status is set.
func (t *tracker) observe(m Message) []remote.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done || !t.belongs(m) {
		return nil
	}
	var out []remote.Event
	// Our own message: its reactions say the remote saw it (👀), works
	// (⏳) or finished (✅/❌).
	if m.TS == t.sentTS {
		hourglass, finished := false, false
		for _, r := range m.Reactions {
			switch {
			case r == "eyes":
				out = t.setStatus(out, remote.StatusThinking, "")
			case strings.HasPrefix(r, "hourglass"):
				hourglass = true
			case r == "white_check_mark" || r == "heavy_check_mark" || r == "x":
				finished = true
			}
		}
		t.hourglass = hourglass
		out = t.busyStatus(out)
		if finished {
			visible, _ := t.compose()
			t.done = true
			out = append(out, remote.Event{Kind: remote.EventDone, Text: visible})
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
	// The status is read from a message's metadata (event_type
	// agent_status) only. Slack sends no event when another app sets its
	// assistant thread status (assistant.threads.setStatus), so that
	// status is not seen here.
	switch strings.ToLower(m.Status) {
	case "thinking", "working", "tool":
		t.statusTS = m.TS
	case "done", "completed", "complete":
		t.statusTS, finished = "", true
	default:
		// The status this message carried was cleared: the remote is done.
		if t.statusTS == m.TS {
			t.statusTS, finished = "", true
		}
	}
	// An edit replaces the message's text; a deletion takes it out of the
	// reply; a progress note — or an answer edited back into one — is a
	// status label, never reply text.
	text := t.clean(m.Text)
	if label, ok := progressLine(text); ok && !m.Deleted {
		delete(t.msgs, m.TS)
		t.label = label
		if !tsLess(m.TS, t.progressTS) {
			t.progressTS = m.TS
		}
	} else {
		if t.progressTS == m.TS {
			t.progressTS = ""
		}
		if text != "" && !m.Deleted {
			t.msgs[m.TS] = text
		} else {
			delete(t.msgs, m.TS)
		}
	}
	out = t.busyStatus(out)
	return t.report(out, finished)
}

// busy: the remote shows it is still working.
func (t *tracker) busy() bool {
	if t.hourglass || t.statusTS != "" {
		return true
	}
	if t.progressTS == "" {
		return false
	}
	for ts := range t.msgs {
		if tsLess(t.progressTS, ts) {
			return false // an answer came after the progress note
		}
	}
	return true
}

// busyStatus reports a change of busy: working (with the progress label)
// or idle, which lets the runner's idle window run again.
func (t *tracker) busyStatus(out []remote.Event) []remote.Event {
	if t.busy() {
		label := ""
		if t.progressTS != "" {
			label = t.label
		}
		return t.setStatus(out, remote.StatusWorking, label)
	}
	if t.status != "" && t.status != remote.StatusThinking {
		return t.setStatus(out, remote.StatusIdle, "")
	}
	return out
}

// report passes the reply on whenever it changed and ends the turn on
// the marker or a done status.
func (t *tracker) report(out []remote.Event, finished bool) []remote.Event {
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
	if t.status == status+detail {
		return out
	}
	t.status = status + detail
	return append(out, remote.Event{Kind: remote.EventStatus, Status: status, Detail: detail})
}

// prune drops the replies a full read of the thread no longer has: they
// were deleted while no event said so.
func (t *tracker) prune(seen map[string]bool) []remote.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done {
		return nil
	}
	gone := false
	for ts := range t.msgs {
		if !seen[ts] {
			delete(t.msgs, ts)
			gone = true
		}
	}
	if !gone {
		return nil
	}
	return t.report(nil, false)
}

var (
	slackLink    = regexp.MustCompile(`<((?:https?|mailto):[^|>]+)(?:\|([^>]*))?>`)
	slackMention = regexp.MustCompile(`^<@([A-Z0-9]+)(?:\|[^>]*)?>\s*`)
)

// clean reads Slack's mrkdwn back as plain text: a leading mention of
// wick's own identity goes, a link becomes "label (url)", and the three
// escaped characters are restored.
func (t *tracker) clean(text string) string {
	text = strings.TrimSpace(text)
	if m := slackMention.FindStringSubmatch(text); m != nil && t.ignore[m[1]] {
		text = text[len(m[0]):]
	}
	text = slackLink.ReplaceAllStringFunc(text, func(s string) string {
		m := slackLink.FindStringSubmatch(s)
		if m[2] == "" || m[2] == m[1] {
			return m[1]
		}
		return m[2] + " (" + m[1] + ")"
	})
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
}

// compose joins the replies in order and handles the marker: a reply
// ending with the turn's token ends the turn and the token is dropped; a
// final line that may still grow into it is held back. A reply that a
// later one starts with (an opening the final message repeats) is left
// out, so it is not there twice.
func (t *tracker) compose() (string, bool) {
	keys := make([]string, 0, len(t.msgs))
	for k := range t.msgs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return tsLess(keys[i], keys[j]) })
	parts := make([]string, 0, len(keys))
	for i, k := range keys {
		repeated := false
		for _, later := range keys[i+1:] {
			if strings.HasPrefix(t.msgs[later], t.msgs[k]) {
				repeated = true
				break
			}
		}
		if !repeated {
			parts = append(parts, t.msgs[k])
		}
	}
	if t.token == "" {
		return strings.Join(parts, "\n\n"), false
	}
	marker := MarkerPrefix + t.token
	// A marker already seen — in a message before the last, or anywhere
	// once a grace window reopened the turn — is dropped, never shown,
	// and does not end the turn again.
	for i := range parts {
		if i < len(parts)-1 || t.reopened {
			if body := strings.TrimRight(parts[i], " \n*_`~"); strings.HasSuffix(body, marker) {
				parts[i] = strings.TrimRight(body[:len(body)-len(marker)], " \n*_`~")
			}
		}
	}
	text := strings.Join(parts, "\n\n")
	if t.reopened {
		return text, false
	}
	// Some bots post rich text whose plain form runs the lines together,
	// so the marker can close the last line instead of standing alone.
	if body := strings.TrimRight(text, " \n*_`~"); strings.HasSuffix(body, marker) {
		return strings.TrimRight(body[:len(body)-len(marker)], " \n*_`~"), true
	}
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

// progressLine reads a progress note that a message is made of alone: a
// wick-style footer ("Thinking…", "_Bash: ls_", ":hourglass: Working"),
// or one short italic or trailing-ellipsis line ("_checking the repo…_").
// Its label becomes the turn's status, never part of the reply.
func progressLine(text string) (string, bool) {
	if text == "" || strings.Contains(text, "\n") || len(text) > 120 {
		return "", false
	}
	if inner := strings.TrimSpace(strings.Trim(text, "_")); inner != "" && !strings.Contains(inner, "_") &&
		(len(text) > 2 && text[0] == '_' && text[len(text)-1] == '_' || strings.HasSuffix(inner, "…") || strings.HasSuffix(inner, "...")) {
		if label, ok := wickProgress(inner); ok {
			return label, true
		}
		return inner, true
	}
	return wickProgress(text)
}

func wickProgress(text string) (string, bool) {
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
