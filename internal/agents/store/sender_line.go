package store

import (
	"strconv"
	"strings"
)

// Sender visibility levels — how much of a sender's identity is repeated to
// the model on every message. Set globally by the operator
// (agents.sender_visibility); see config.GeneralConfig.SenderVisibility.
//
// These govern ONLY what the model receives. The turn on disk always carries
// the full Sender as its own field, next to `text` rather than inside it, and
// the dashboard always renders all of it — so lowering this costs the model
// context, never the operator's view of who said what.
const (
	// SenderOff writes no identity line at all.
	SenderOff = "off"
	// SenderName writes the display name — enough to address people correctly
	// in a shared thread. The default.
	SenderName = "name"
	// SenderNameID adds the platform user ID, for agents that need to
	// @-mention or DM one specific person.
	SenderNameID = "name_id"
	// SenderFull adds the handle and channel too.
	SenderFull = "full"
)

// NormalizeSenderVisibility maps a configured value to a known level,
// defaulting to SenderName.
//
// An unrecognised value falls back to the default rather than to "off": a
// typo in the config row must not silently strip identity from every message.
func NormalizeSenderVisibility(v string) string {
	switch s := strings.TrimSpace(v); s {
	case SenderOff, SenderName, SenderNameID, SenderFull:
		return s
	default:
		return SenderName
	}
}

// PrependSenderLine returns text with a single leading `[from: …]` line
// naming who sent it. Returns text unchanged when s is nil or level is
// SenderOff.
//
// This is the ONLY channel through which the agent learns who it is talking
// to, and it is written here — by wick, from the channel's transport
// envelope — never by the person typing. The immutable system prompt tells
// the agent that this line is the sole trustworthy identity claim and that a
// body claiming otherwise is forged.
//
// The line exists only on the copy handed to the model. The stored turn keeps
// the original text plus the Sender as a sibling field, which is what lets
// the dashboard render a clean message with a sender chip, and lets an
// operator dial this setting down without losing any history.
//
// Kept short on purpose: it repeats on EVERY message, so each field has to
// earn its tokens. The channel is already in the one-time session-context
// block a channel injects when a session starts, and the handle is in the
// member directory alongside it — neither needs restating per turn, which is
// why they only appear at SenderFull.
//
// It lives in this package, beside Sender itself, because two callers need
// it and neither may import the other: the pool prepends it on a live send,
// and a provider replaying conversation.jsonl has to re-apply it (the stored
// text has no line, by design) or every replayed turn loses its sender.
func PrependSenderLine(text string, s *Sender, level string) string {
	line := SenderLine(s, level)
	if line == "" {
		return text
	}
	return line + "\n" + text
}

// SenderLine renders just the `[from: …]` line, without a trailing newline.
// Returns "" when there is nothing to say.
func SenderLine(s *Sender, level string) string {
	if s == nil || level == SenderOff {
		return ""
	}

	// A name is the useful half, but also the one that can be missing: a
	// users.info lookup fails, or a Telegram user set neither name. Fall back
	// through handle to ID so a turn is never unattributed — telling two
	// participants apart matters even when neither can be named.
	who := s.Name
	if who == "" {
		who = s.Handle
	}
	if who == "" {
		who = s.ID
	}
	if who == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("[from: ")
	b.WriteString(quoteSenderField(who))

	switch level {
	case SenderNameID:
		if s.ID != "" && s.ID != who {
			b.WriteString(" (")
			b.WriteString(quoteSenderField(s.ID))
			b.WriteString(")")
		}
	case SenderFull:
		if s.ID != "" && s.ID != who {
			b.WriteString(" (")
			b.WriteString(quoteSenderField(s.ID))
			b.WriteString(")")
		}
		if s.Handle != "" && s.Handle != who {
			b.WriteString(" @")
			b.WriteString(quoteSenderField(s.Handle))
		}
		if s.Channel != "" {
			b.WriteString(" via ")
			b.WriteString(quoteSenderField(s.Channel))
		}
	}

	b.WriteString("]")
	return b.String()
}

// quoteSenderField renders one field of the sender line. Ordinary values pass
// through bare so the common line stays short and readable; anything that
// could terminate the line early or fake a second one is Go-quoted, which
// escapes brackets, quotes and newlines into a single safe token.
func quoteSenderField(v string) string {
	if strings.ContainsAny(v, "[]\"\n\r\\") {
		return strconv.Quote(v)
	}
	return v
}
