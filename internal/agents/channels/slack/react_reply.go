package slack

import (
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
)

// A reply that is only "[react :pray:]" (or "[react:pray]") becomes a
// reaction on the message that woke the turn instead of a post: a thank-you
// gets an emoji, not a sentence. The marker may also open a longer reply on a
// line of its own: the reaction is added and the rest is posted as usual. An
// explicit ts ("[react :pray: 1700000000.000100]") targets that message of the
// same channel instead, for a turn that answers several held messages.
var reactReplyRe = regexp.MustCompile(`^\[react[:\s]\s*:?([a-z0-9_+'\-]+(?:::skin-tone-[2-6])?):?(?:\s+(\d+\.\d+))?\s*\]$`)

// reactMarker splits a reply whose first line is a react marker into the
// emoji name, the optional target ts and the text after that line.
func reactMarker(text string) (name, ts, rest string, ok bool) {
	text = strings.TrimSpace(text)
	first, rest, _ := strings.Cut(text, "\n")
	m := reactReplyRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(first)))
	if m == nil {
		return "", "", text, false
	}
	return m[1], m[2], strings.TrimSpace(rest), true
}

// reactOnlyReply reports the emoji name when text is just a react marker.
func reactOnlyReply(text string) (string, bool) {
	name, _, rest, ok := reactMarker(text)
	if !ok || rest != "" {
		return "", false
	}
	return name, true
}

// dropReactLine returns text without a leading react marker line, so the
// marker never reaches the thread as literal text.
func dropReactLine(text string) string {
	if _, _, rest, ok := reactMarker(text); ok {
		return rest
	}
	return text
}

// reactPending reports a streaming reply that may still turn out to be a
// react marker: it is held back from the live message until the turn ends.
func reactPending(body string) bool {
	t := strings.ToLower(strings.TrimSpace(body))
	return strings.HasPrefix(t, "[react") || (t != "" && strings.HasPrefix("[react", t))
}

// reactInsteadOfReply puts name on the turn's waking message (the thread's
// top when that is unknown) and drops a live message a stream may have
// posted already.
func (s *Channel) reactInsteadOfReply(sessionKey, channelID, threadTS, liveTS, name string) {
	s.mu.Lock()
	if cur := s.turns[sessionKey]; cur != nil {
		cur.liveTS, cur.lastSent = "", ""
	}
	s.mu.Unlock()
	s.reactTo(sessionKey, channelID, threadTS, liveTS, "", name)
}

// reactTo puts name on targetTS, or on the turn's waking message (the
// thread's top when that is unknown) when targetTS is empty. A non-empty
// liveTS is deleted first.
func (s *Channel) reactTo(sessionKey, channelID, threadTS, liveTS, targetTS, name string) {
	msgTS := targetTS
	if msgTS == "" {
		s.mu.Lock()
		if cur := s.turns[sessionKey]; cur != nil {
			msgTS = cur.msgTS
		}
		s.mu.Unlock()
	}
	if msgTS == "" {
		msgTS = threadTS
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || channelID == "" || msgTS == "" {
		return
	}
	if liveTS != "" {
		_, _, _ = api.DeleteMessage(channelID, liveTS)
	}
	err := api.AddReaction(name, slackgo.ItemRef{Channel: channelID, Timestamp: msgTS})
	if err != nil && !strings.Contains(err.Error(), "already_reacted") {
		log.Warn().Str("channel", "slack").Str("slack_channel", channelID).Str("ts", msgTS).
			Str("emoji", name).Err(err).Msg("react reply: could not add the reaction")
		return
	}
	log.Info().Str("channel", "slack").Str("slack_channel", channelID).Str("ts", msgTS).
		Str("emoji", name).Msg("react reply: answered with a reaction")
}
