package slack

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Reply modes of a 🤖 thread, per channel (ChannelReplyRules) over the
// bot-wide ReplyMode.
//
//   - listen: a reply is held for the listen delay, then the agent answers
//     only what is meant for it ([silent] otherwise);
//   - auto_reply: a reply is held only for the merge delay (a burst becomes
//     one turn), then always answered, like a mention;
//   - manual: listen, but a new thread never starts on its own — the
//     channel-rule form of ListenManualChannels.
const (
	replyModeListen = "listen"
	replyModeAuto   = "auto_reply"
	replyModeManual = "manual"

	// autoReplyMaxHold caps an auto-reply merge: a thread that never goes
	// quiet still reaches the agent this long after its first reply.
	autoReplyMaxHold = 30 * time.Second
	// defaultAutoReplyDelay is the merge delay when the setting is empty.
	defaultAutoReplyDelay = 3 * time.Second

	// reactionHeld marks a reply wick is holding, from the moment it
	// arrives until it goes to the agent or is dropped.
	reactionHeld = "eyes" // 👀
)

// replyRule is one row of ChannelReplyRules.
type replyRule struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Mode  string `json:"mode"`
	Delay string `json:"delay"`
}

// channelRule is channelID's row in cfg's rules, if it has one.
func channelRule(cfg agentconfig.SlackChannelConfig, channelID string) (replyRule, bool) {
	if cfg.ChannelReplyRules == "" || channelID == "" {
		return replyRule{}, false
	}
	var rows []replyRule
	if err := json.Unmarshal([]byte(cfg.ChannelReplyRules), &rows); err != nil {
		return replyRule{}, false
	}
	for _, r := range rows {
		if r.ID == channelID {
			r.Mode = normReplyMode(r.Mode)
			return r, true
		}
	}
	return replyRule{}, false
}

// normReplyMode maps a stored mode onto the three known ones; anything
// else (empty, a typo) is "".
func normReplyMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case replyModeListen:
		return replyModeListen
	case replyModeAuto, "auto-reply", "autoreply", "auto":
		return replyModeAuto
	case replyModeManual:
		return replyModeManual
	}
	return ""
}

// replyModeFor is channelID's mode: its rule, else the bot-wide default,
// else listen.
func replyModeFor(cfg agentconfig.SlackChannelConfig, channelID string) string {
	if r, ok := channelRule(cfg, channelID); ok && r.Mode != "" {
		return r.Mode
	}
	if m := normReplyMode(cfg.ReplyMode); m == replyModeAuto {
		return m
	}
	return replyModeListen
}

// channelDelay is the delay channelID's rule sets, if it sets one.
func channelDelay(cfg agentconfig.SlackChannelConfig, channelID string) (time.Duration, bool) {
	if r, ok := channelRule(cfg, channelID); ok {
		return parseListenDelay(r.Delay)
	}
	return 0, false
}

// autoReplyDelay is channelID's merge delay: its rule, else the setting,
// else the built-in default. Never above autoReplyMaxHold.
func autoReplyDelay(cfg agentconfig.SlackChannelConfig, channelID string) time.Duration {
	d, ok := channelDelay(cfg, channelID)
	if !ok {
		d, ok = parseListenDelay(cfg.AutoReplyDelaySec)
	}
	if !ok {
		d = defaultAutoReplyDelay
	}
	if d > autoReplyMaxHold {
		d = autoReplyMaxHold
	}
	return d
}

// listenOverrides are the delays settings lay over the LISTEN.md chain:
// cfg goes over the wick document and under the agent's, channel over the
// agent's and under the thread's own request.
type listenOverrides struct {
	cfg, channel *time.Duration
}

// listenOverridesFor reads them for channelID ("" = no channel rule).
func listenOverridesFor(cfg agentconfig.SlackChannelConfig, channelID string) listenOverrides {
	var ov listenOverrides
	if d, ok := parseListenDelay(cfg.ListenDelaySec); ok {
		ov.cfg = &d
	}
	if d, ok := channelDelay(cfg, channelID); ok {
		ov.channel = &d
	}
	return ov
}

// Held-reply states, as the listen_held system turn reports them.
const (
	heldStateHeld      = "held"
	heldStateSent      = "sent"
	heldStateSkipped   = "skipped"
	heldStateCancelled = "cancelled"
)

// HeldFn records where a held reply stands in its session's timeline, so
// the web UI shows it from the moment it arrives instead of only once it
// reaches the agent. extras carry hold_id (the fold key), state, mode,
// delay_sec, user and msg_ts.
type HeldFn func(sessionID, text string, extras map[string]string)

// SetHeldFn wires the recorder of held replies.
func (s *Channel) SetHeldFn(fn HeldFn) {
	s.mu.Lock()
	s.heldFn = fn
	s.mu.Unlock()
}

// reportHeld records w's state for the web UI. Best effort: no recorder
// wired means nothing is shown, never that the reply is lost.
func (s *Channel) reportHeld(sessionID string, w *listenWait, state, why string) {
	s.mu.Lock()
	fn := s.heldFn
	s.mu.Unlock()
	if fn == nil || w == nil || w.ev == nil {
		return
	}
	modeLabel := "Listen"
	if w.auto {
		modeLabel = "Auto-reply"
	}
	who := w.ev.User
	if who == "" {
		who = w.ev.BotID
	}
	var text string
	switch state {
	case heldStateHeld:
		text = fmt.Sprintf("%s · holding a reply from <@%s> for %s: “%s”", modeLabel, who, w.delay.Round(time.Second), excerpt(w.ev.Text, 80))
	case heldStateSent:
		text = fmt.Sprintf("%s · passed to the agent after %s", modeLabel, time.Since(w.started).Round(time.Second))
	case heldStateSkipped:
		text = fmt.Sprintf("%s · not passed on: %s", modeLabel, why)
	default:
		text = fmt.Sprintf("%s · dropped: %s", modeLabel, why)
	}
	mode := replyModeListen
	if w.auto {
		mode = replyModeAuto
	}
	fn(sessionID, text, map[string]string{
		"hold_id":    w.holdID,
		"state":      state,
		"mode":       mode,
		"delay_sec":  fmt.Sprintf("%d", int(w.delay/time.Second)),
		"started_ms": fmt.Sprintf("%d", w.started.UnixMilli()),
		"user":       who,
		"msg_ts":     w.ev.TimeStamp,
	})
}

// excerpt is the first n runes of text on one line, with an ellipsis when
// cut.
func excerpt(text string, n int) string {
	t := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(t) <= n {
		return t
	}
	r := []rune(t)
	return string(r[:n]) + "…"
}
