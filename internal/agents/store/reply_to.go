package store

import (
	"context"
	"strings"
	"unicode"
)

// ReplyExcerptMax bounds the quoted text kept on a reply and handed to the
// provider, in runes. The UI shows a shorter one-line preview of it.
const ReplyExcerptMax = 500

// replyAuthorMax bounds the author name inside the quote line, in runes.
const replyAuthorMax = 64

// ReplyTo is the message a user turn answers: a "Reply" on one bubble of
// the web chat. Only the send endpoint sets it, after checking the turn
// exists in the same session, and it builds Author and Excerpt from the
// stored turn itself — never from anything the client sent.
type ReplyTo struct {
	// TurnID is the quoted turn's id as the conversation API reports it
	// (a stored id, or the "turn-<n>" position older turns are given).
	TurnID  string `json:"turn_id"`
	Role    string `json:"role,omitempty"`
	Author  string `json:"author,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

type replyToKey struct{}

// scopedReply binds a reply to the one session it was checked against.
type scopedReply struct {
	sessionID string
	reply     *ReplyTo
}

// WithReplyTo marks the user turn sent into sessionID with ctx as a reply
// to r. Bound to that session: the same ctx reaching another session (an
// @mention routed to a sub-agent or teammate) carries no reply there, so a
// quote can never leave the chat it was checked in.
func WithReplyTo(ctx context.Context, sessionID string, r *ReplyTo) context.Context {
	return context.WithValue(ctx, replyToKey{}, scopedReply{sessionID: sessionID, reply: r})
}

// ReplyToFrom returns the reply WithReplyTo put on ctx for sessionID, or nil.
func ReplyToFrom(ctx context.Context, sessionID string) *ReplyTo {
	v, _ := ctx.Value(replyToKey{}).(scopedReply)
	if v.sessionID == "" || v.sessionID != sessionID {
		return nil
	}
	return v.reply
}

type userTurnIDKey struct{}

type scopedTurnID struct{ sessionID, turnID string }

// WithUserTurnID fixes the id the user turn sent into sessionID with ctx is
// stored under, so the endpoint that sent it can hand the id back. Bound to
// that session like WithReplyTo.
func WithUserTurnID(ctx context.Context, sessionID, turnID string) context.Context {
	return context.WithValue(ctx, userTurnIDKey{}, scopedTurnID{sessionID: sessionID, turnID: turnID})
}

// UserTurnIDFrom returns the id WithUserTurnID put on ctx for sessionID, or "".
func UserTurnIDFrom(ctx context.Context, sessionID string) string {
	v, _ := ctx.Value(userTurnIDKey{}).(scopedTurnID)
	if v.sessionID == "" || v.sessionID != sessionID {
		return ""
	}
	return v.turnID
}

// ReplyExcerpt turns a stored message into the text a reply quotes: one
// line, control characters dropped, at most ReplyExcerptMax runes.
func ReplyExcerpt(text string) string {
	return clipRunes(oneLine(text), ReplyExcerptMax)
}

// PrependReplyQuote puts the quote line in front of the text the provider
// reads, so the agent knows which message the person is answering:
//
//	> Replying to <author>: "<excerpt>"
//
// The quote is the user's own data, not an instruction, so it is defused
// before it goes in: one line (a quoted message cannot open a line that
// reads as a `[from: …]`, `[Attached files]` or tool marker), control
// characters dropped, brackets and angle brackets swapped for look-alikes
// (no `<system-reminder>`, no `[wick-…]` tag survives), and bounded.
// Stored text never carries the line; a history replay re-applies it.
func PrependReplyQuote(text string, r *ReplyTo) string {
	if r == nil || IsBareSlashCommand(text) {
		return text
	}
	author := clipRunes(neutralizeQuote(oneLine(r.Author)), replyAuthorMax)
	if author == "" {
		author = "a message"
	}
	excerpt := clipRunes(neutralizeQuote(oneLine(r.Excerpt)), ReplyExcerptMax)
	return "> Replying to " + author + `: "` + excerpt + `"` + "\n\n" + text
}

// oneLine collapses every run of whitespace (newlines included) into one
// space and drops other control and format characters.
func oneLine(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = true
			continue
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

var quoteNeutralizer = strings.NewReplacer(
	"[", "［", "]", "］",
	"<", "‹", ">", "›",
	`"`, "'",
	"`", "'",
)

func neutralizeQuote(s string) string {
	return quoteNeutralizer.Replace(s)
}

func clipRunes(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return strings.TrimSpace(string(rs[:max-1])) + "…"
}
