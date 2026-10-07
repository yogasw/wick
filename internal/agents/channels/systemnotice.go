package channels

import (
	"github.com/yogasw/wick/internal/agents/event"
)

// SystemNoticeText renders the line a chat channel should post for an
// event that is ABOUT the session rather than part of the agent's reply,
// and reports whether this event is one of those at all.
//
// The web UI gets these for free: it reads the conversation file, so a
// system turn (a compaction boundary today) shows up as its own row next
// to the messages. A chat thread has no such row — it only ever sees what
// a channel decides to post — so from Slack or Telegram the same event is
// simply invisible. Someone who asks for /compact from a thread watches
// the agent say it queued one and then never hears that it happened.
//
// Keeping the mapping here rather than in each channel means the next
// system event worth surfacing is added once and reaches every transport,
// and that the wording cannot drift between them. Returning false is the
// normal answer: reply traffic (deltas, tool activity, Done) is the
// channel's own business and must not be duplicated as a notice.
func SystemNoticeText(ev event.AgentEvent) (string, bool) {
	switch ev.Type {
	case event.Compaction:
		if ev.Compaction == nil {
			return "", false
		}
		return ev.Compaction.Summary(), true
	default:
		return "", false
	}
}
