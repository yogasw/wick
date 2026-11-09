package channels

import (
	"github.com/yogasw/wick/internal/agents/event"
)

// SystemNoticeText renders the line a chat channel should post for an
// event that is ABOUT the session rather than part of the agent's reply,
// and reports whether this event is one of those at all.
//
// Only wick's own notices qualify. A compaction boundary does not: it is
// bookkeeping about the agent's context window, which means nothing to
// the people in a Slack or Telegram thread and reads there as a stray
// system line wedged above the agent's answer. The web UI still shows it
// as its own row, read from the conversation file, so nothing is lost.
//
// Keeping the mapping here rather than in each channel means the next
// system event worth surfacing is added once and reaches every transport,
// and that the wording cannot drift between them. Returning false is the
// normal answer: reply traffic (deltas, tool activity, Done) is the
// channel's own business and must not be duplicated as a notice.
func SystemNoticeText(ev event.AgentEvent) (string, bool) {
	switch ev.Type {
	case event.Notice:
		return ev.Text, ev.Text != ""
	default:
		return "", false
	}
}
