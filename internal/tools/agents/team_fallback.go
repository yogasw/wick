package agents

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// When a Team agent is woken with its teammates' results and its turn
// ends with nothing for the channels (empty, or [silent]), the results
// would never reach the thread. The channel registry tells
// TeamFallbackNotice about every turn end; it answers only for a turn the
// teamlink Hub woke (a wake mark made before that turn started, see
// teamlink.TakeWakes), so any other quiet turn costs a map lookup. The
// line is built from the wake prompt itself: one per reply it carries.

const (
	// teamFallbackPreview is how many characters of each reply the notice
	// keeps; teamFallbackMax caps the whole line (a Slack context block
	// takes 3000).
	teamFallbackPreview = 300
	teamFallbackMax     = 2800
)

var (
	// The framed reply of a batch or a lone reply (teamlink frameData).
	teamFallbackReply = regexp.MustCompile(`(?s)<reply from="@([^"]*)" task="[^"]*" state="([^"]*)">\n?(.*?)\n?</reply>`)
	// singleText: "Reply from Name (@h) [task id, state]:\n\n<text>".
	teamFallbackSingle = regexp.MustCompile(`(?s)^Reply from (.+?) \[task [^,\]\n]+, ([^\]\n]+)\]:\n\n?(.*)$`)
	// A follow-up after fan_in first answered.
	teamFallbackFollow = regexp.MustCompile(`(?s)^Follow-up from (.+?) \[task [^\]\n]*\]:\n<follow-up>\n?(.*?)\n?</follow-up>`)
	teamFallbackHandle = regexp.MustCompile(`\(@([a-z0-9][a-z0-9-]*)\)\s*$`)
)

// TeamFallbackNotice is the channel line for sessionID's turn that started
// at start and just ended, when that turn answered a prompt of the Hub and
// was quiet; false for any other turn. Every turn end consumes the wake
// marks it answered, quiet or not. Wired into the channel registry
// (WithTurnFallback).
func TeamFallbackNotice(sessionID string, start time.Time, quiet bool) (string, bool) {
	if globalTeamHub == nil || sessionID == "" || start.IsZero() {
		return "", false
	}
	h := globalTeamHub()
	if h == nil {
		return "", false
	}
	since, ok := h.TakeWakes(sessionID, start)
	if !ok || !quiet || globalLayout.BaseDir == "" {
		return "", false
	}
	turns, err := loadConversation(globalLayout, sessionID)
	if err != nil {
		return "", false
	}
	text, ok := wakeTextOf(turns, since, start)
	if !ok {
		return "", false
	}
	return teamFallbackText(text, !teamShowsTeammatesCached(sessionID))
}

// wakeTextOf picks the prompt the turn that started at start answered: the
// newest Hub prompt (a user turn, source "subagent") stored between since
// (the turn's earliest wake mark) and start. A person's message stored
// after start is the next turn's, an assistant turn after the prompt means
// an earlier turn already answered it.
func wakeTextOf(turns []store.ConversationTurn, since, start time.Time) (string, bool) {
	slack := time.Second // the mark is taken just before the prompt is stored
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		if t.Timestamp.After(start) {
			continue
		}
		if t.Timestamp.Before(since.Add(-slack)) {
			return "", false
		}
		if t.Role == "assistant" {
			return "", false
		}
		if t.Role == "user" && strings.EqualFold(strings.TrimSpace(t.Source), "subagent") {
			return t.Text, true
		}
	}
	return "", false
}

// teamFallbackText is the notice for a wake prompt, in every shape the Hub
// delivers: the batched results and their late update, a lone reply
// (singleText, or framed for fan_in each), and a follow-up. One line per
// reply: who, its state, the start of its text. A question is not a
// result: none. hide = the agent's "Show teammates in channels" is off,
// so nobody is named. The whole line is capped; each channel escapes it
// for its own markup (Slack: slack.go).
func teamFallbackText(text string, hide bool) (string, bool) {
	text = strings.TrimSpace(text)
	type item struct{ who, state, body string }
	var items []item
	switch {
	case strings.HasPrefix(text, "[team results"), strings.HasPrefix(text, "Reply from "):
		for _, m := range teamFallbackReply.FindAllStringSubmatch(text, -1) {
			items = append(items, item{"@" + m[1], m[2], m[3]})
		}
		if len(items) == 0 {
			if m := teamFallbackSingle.FindStringSubmatch(text); m != nil {
				who := m[1]
				if h := teamFallbackHandle.FindStringSubmatch(who); h != nil {
					who = "@" + h[1]
				}
				items = append(items, item{who, m[2], m[3]})
			}
		}
	case strings.HasPrefix(text, "Follow-up from "):
		if m := teamFallbackFollow.FindStringSubmatch(text); m != nil {
			who := m[1]
			if h := teamFallbackHandle.FindStringSubmatch(who); h != nil {
				who = "@" + h[1]
			}
			items = append(items, item{who, "follow-up", m[2]})
		}
	}
	var keep []item
	for _, it := range items {
		if !strings.Contains(it.state, "input") {
			keep = append(keep, it)
		}
	}
	if len(keep) == 0 {
		return "", false
	}
	var b strings.Builder
	if hide {
		b.WriteString("Results came in, but the agent gave no summary:")
	} else {
		b.WriteString("Team results (the agent gave no summary):")
	}
	for i, it := range keep {
		body := strings.Join(strings.Fields(unframe(it.body)), " ")
		if r := []rune(body); len(r) > teamFallbackPreview {
			body = string(r[:teamFallbackPreview-1]) + "…"
		}
		if body == "" {
			body = "(no text)"
		}
		who := it.who
		if hide {
			who = fmt.Sprintf("Result %d", i+1)
		}
		line := fmt.Sprintf("\n• %s — %s: %s", who, it.state, body)
		if b.Len()+len(line) > teamFallbackMax {
			fmt.Fprintf(&b, "\n… and %d more", len(keep)-i)
			break
		}
		b.WriteString(line)
	}
	return b.String(), true
}

// unframe undoes teamlink's frameData on a reply ("<\/reply" back to
// "</reply", the "(quoted) " mark off a header-like line).
func unframe(s string) string {
	s = strings.ReplaceAll(s, `<\/`, "</")
	return strings.ReplaceAll(s, "(quoted) ", "")
}
