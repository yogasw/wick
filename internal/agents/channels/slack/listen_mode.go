package slack

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	slackgo "github.com/slack-go/slack"

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

// withChannelRuleMode is rulesJSON with channelID's row set to mode: its
// name and delay kept, a new row (named name) appended when there is none.
func withChannelRuleMode(rulesJSON, channelID, name, mode string) (string, error) {
	var rows []replyRule
	if strings.TrimSpace(rulesJSON) != "" {
		if err := json.Unmarshal([]byte(rulesJSON), &rows); err != nil {
			return "", fmt.Errorf("channel rules are not valid JSON: %w", err)
		}
	}
	found := false
	for i := range rows {
		if rows[i].ID == channelID {
			rows[i].Mode = mode
			found = true
		}
	}
	if !found {
		rows = append(rows, replyRule{ID: channelID, Name: name, Mode: mode})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// channelNames caches conversations.info names for the composer's mode
// button, which re-reads the state every 30s.
var channelNames sync.Map

// channelName is channelID's name: its rule's, else Slack's, else "".
func (s *Channel) channelName(cfg agentconfig.SlackChannelConfig, channelID string) string {
	if r, ok := channelRule(cfg, channelID); ok && r.Name != "" {
		return strings.TrimPrefix(r.Name, "#")
	}
	if v, ok := channelNames.Load(channelID); ok {
		return v.(string)
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return ""
	}
	info, err := api.GetConversationInfo(&slackgo.GetConversationInfoInput{ChannelID: channelID})
	if err != nil || info == nil || info.Name == "" {
		return ""
	}
	channelNames.Store(channelID, info.Name)
	return info.Name
}

// threadReplyMode is how a reply in sessionID's thread is treated:
// manual when the thread is not listening, else its channel's mode (a
// manual channel rule only stops new threads, so a listening thread in it
// listens). holdSec is what a reply is held for in that mode.
func (s *Channel) threadReplyMode(sessionID, channelID string) (mode string, holdSec int) {
	cfg := s.snapshot()
	if !s.listening(sessionID, channelID) {
		return replyModeManual, 0
	}
	if replyModeFor(cfg, channelID) == replyModeAuto {
		return replyModeAuto, int(autoReplyDelay(cfg, channelID) / time.Second)
	}
	return replyModeListen, int(s.listenSettingsIn(sessionID, channelID).Delay / time.Second)
}

// ReplyModeRules plans the composer's mode switch for sessionID's thread:
// the channel rules to store when mode needs its channel's mode changed
// (changed = false: the channel already behaves so, or mode is manual,
// which only stops this thread). Nothing is applied.
func (s *Channel) ReplyModeRules(sessionID, mode string) (rules string, changed bool, err error) {
	mode = normReplyMode(mode)
	if mode == "" {
		return "", false, fmt.Errorf("mode must be auto_reply, listen or manual")
	}
	b, err := s.threadBinding(sessionID)
	if err != nil {
		return "", false, err
	}
	if mode == replyModeManual {
		return "", false, nil
	}
	cfg := s.snapshot()
	if (replyModeFor(cfg, b.ChatID) == replyModeAuto) == (mode == replyModeAuto) {
		return "", false, nil
	}
	rules, err = withChannelRuleMode(cfg.ChannelReplyRules, b.ChatID, s.channelName(cfg, b.ChatID), mode)
	return rules, err == nil, err
}

// ApplyReplyMode carries the switch out once the caller stored rules
// (when changed): the rules take effect at once, then the thread listens
// (auto_reply, listen) with its own prompt and delay kept, or stops
// (manual). Nothing reverts on its own.
func (s *Channel) ApplyReplyMode(sessionID, mode, rules string, changed bool) (ListenResult, error) {
	if changed {
		s.cfgMu.Lock()
		s.cfg.ChannelReplyRules = rules
		s.cfgMu.Unlock()
	}
	req := ListenRequest{Action: "off"}
	if normReplyMode(mode) != replyModeManual {
		req = ListenRequest{Action: "on"}
		if ls, ok := s.listenStore(); ok {
			lt := ls.ListenThread(sessionID)
			req.Instructions, req.DelaySec = lt.Prompt, lt.DelaySec
		}
	}
	return s.ThreadListen(sessionID, req)
}
