package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

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
	// reactionWorking marks the replies a held turn is working on, from
	// the moment they go to the agent until its turn ends.
	reactionWorking = "hourglass_flowing_sand" // ⏳
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
	// heldStateMerged: a mention answered the thread before the delay ran
	// out, and that turn already reads the held message. Not a problem — the
	// message did reach the agent, just through the mention.
	heldStateMerged = "merged"
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

// SetHeardFn wires the recorder of the replies a listening thread hears:
// each one lands in its session the moment it arrives, as the conversation
// it is, before (and whether or not) the agent reads it. extras carry
// hold_id, msg_ts, user and name; the text is the reply itself.
func (s *Channel) SetHeardFn(fn HeldFn) {
	s.mu.Lock()
	s.heardFn = fn
	s.mu.Unlock()
}

// reportHeard records one reply a listening thread heard. Best effort, like
// reportHeld: no recorder means nothing is shown, never a lost reply.
func (s *Channel) reportHeard(sessionID string, w *listenWait, ev *slackevents.MessageEvent, files []slackgo.File) {
	s.mu.Lock()
	fn := s.heardFn
	s.mu.Unlock()
	if fn == nil || ev == nil {
		return
	}
	who := ev.User
	if who == "" {
		who = ev.BotID
	}
	name := ev.Username
	if ev.User != "" {
		name = s.heardName(ev.User)
	}
	if name == "" {
		name = who
	}
	extras := map[string]string{
		"msg_ts": ev.TimeStamp,
		"user":   who,
		"name":   name,
	}
	// Whose wick account wrote it, when wick already knows, so the web UI
	// can put the reader's own replies on their side of the thread.
	if wick := s.heardWickUser(ev.User); wick != "" {
		extras["wick_user_id"] = wick
	}
	if ev.BotID != "" {
		extras["bot"] = "1"
	}
	if w != nil {
		extras["hold_id"] = w.holdID
	}
	if len(files) > 0 {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.Name)
		}
		extras["files"] = strings.Join(names, "\n")
	}
	fn(sessionID, s.heardText(ev.Text), extras)
}

// heardName is a Slack user's display name for a heard reply: the sender
// cache when the user already started a turn, else one users.info call,
// cached on its own. It never maps the user to a wick account — a
// passer-by in a thread must not get one — so it stays out of
// userDisplayCache, whose entries carry that mapping. "" when Slack will
// not say.
func (s *Channel) heardName(userID string) string {
	if userID == "" {
		return ""
	}
	s.userDisplayMu.RLock()
	sn := s.userDisplayCache[userID]
	name, cached := s.heardNames[userID]
	s.userDisplayMu.RUnlock()
	if sn != nil && sn.Name != "" {
		return sn.Name
	}
	if cached {
		return name
	}
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return ""
	}
	if u, err := api.GetUserInfo(userID); err == nil && u != nil {
		name = u.RealName
		if name == "" {
			name = u.Profile.DisplayName
		}
		if name == "" {
			name = u.Name
		}
	}
	s.userDisplayMu.Lock()
	if s.heardNames == nil {
		s.heardNames = map[string]string{}
	}
	s.heardNames[userID] = name
	s.userDisplayMu.Unlock()
	return name
}

// heardWickUser is the wick account a heard reply's author maps to, "" when
// wick does not know one. Read-only: the sender cache (a user who started a
// turn) or a Slack account connected to wick; it never registers anyone.
// Cached per user, found or not, so a busy thread costs one lookup each.
func (s *Channel) heardWickUser(userID string) string {
	if userID == "" {
		return ""
	}
	s.userDisplayMu.RLock()
	sn := s.userDisplayCache[userID]
	wick, cached := s.heardWick[userID]
	s.userDisplayMu.RUnlock()
	if sn != nil && sn.WickUserID != "" {
		return sn.WickUserID
	}
	if cached {
		return wick
	}
	s.cfgMu.Lock()
	byOAuth := s.wickUserIDFn
	s.cfgMu.Unlock()
	if byOAuth != nil {
		if id, ok := byOAuth(context.Background(), userID); ok {
			wick = id
		}
	}
	s.userDisplayMu.Lock()
	if s.heardWick == nil {
		s.heardWick = map[string]string{}
	}
	s.heardWick[userID] = wick
	s.userDisplayMu.Unlock()
	return wick
}

// slackMarkup matches one <…> token of Slack's message markup.
var slackMarkup = regexp.MustCompile(`<([^<>]+)>`)

// heardText turns a reply's Slack markup into what the web UI renders:
// <@U…> becomes @Name, <url|label> the link (with its label when that
// says more than the link), <#C…|name> #name,
// <!here> @here, and Slack's &lt; &gt; &amp; plain characters again.
func (s *Channel) heardText(text string) string {
	out := slackMarkup.ReplaceAllStringFunc(text, func(m string) string {
		target, label, _ := strings.Cut(m[1:len(m)-1], "|")
		switch {
		case strings.HasPrefix(target, "@"):
			if label != "" {
				return "@" + label
			}
			if n := s.heardName(target[1:]); n != "" {
				return "@" + n
			}
			return "@" + target[1:]
		case strings.HasPrefix(target, "#"):
			if label != "" {
				return "#" + label
			}
			return "#channel"
		case strings.HasPrefix(target, "!subteam^"):
			if label != "" {
				return label
			}
			return "@group"
		case strings.HasPrefix(target, "!"):
			if label != "" {
				return label
			}
			return "@" + strings.TrimPrefix(target, "!")
		case strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:"):
			// A label that is only the link written shorter adds nothing:
			// the link alone, which the web UI makes clickable.
			bare := label
			if _, after, ok := strings.Cut(bare, "://"); ok {
				bare = after
			}
			bare = strings.TrimRight(bare, "….")
			if label == "" || strings.Contains(target, bare) {
				return target
			}
			return label + " (" + target + ")"
		}
		return m
	})
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(out)
}

// markWorking puts ⏳ on the replies a turn is about to work on; the turn's
// end takes it off (clearWorking).
func (s *Channel) markWorking(sessionID, channelID string, ts []string) {
	if len(ts) == 0 {
		return
	}
	s.mu.Lock()
	if s.working == nil {
		s.working = map[string]workingMark{}
	}
	prev := s.working[sessionID]
	if prev.channel == channelID {
		ts = append(prev.ts, ts...)
	}
	s.working[sessionID] = workingMark{channel: channelID, ts: ts}
	s.mu.Unlock()
	for _, t := range ts {
		s.reaction(reactionWorking, channelID, t, true)
	}
}

// clearWorking takes ⏳ off once sessionID's turn ended, answered or not.
func (s *Channel) clearWorking(sessionID string) {
	s.mu.Lock()
	m, ok := s.working[sessionID]
	delete(s.working, sessionID)
	s.mu.Unlock()
	if !ok {
		return
	}
	for _, t := range m.ts {
		s.reaction(reactionWorking, m.channel, t, false)
	}
}

// workingMark is where a session's ⏳ sits.
type workingMark struct {
	channel string
	ts      []string
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
	case heldStateMerged:
		text = fmt.Sprintf("%s · answered together with the mention", modeLabel)
	default:
		text = fmt.Sprintf("%s · dropped: %s", modeLabel, why)
	}
	mode := replyModeListen
	if w.auto {
		mode = replyModeAuto
	}
	fn(sessionID, text, map[string]string{
		"hold_id":     w.holdID,
		"state":       state,
		"mode":        mode,
		"delay_sec":   fmt.Sprintf("%d", int(w.delay/time.Second)),
		"started_ms":  fmt.Sprintf("%d", w.started.UnixMilli()),
		"deadline_ms": fmt.Sprintf("%d", w.deadline.UnixMilli()),
		"heard":       fmt.Sprintf("%d", w.heard),
		"user":        who,
		"msg_ts":      w.ev.TimeStamp,
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
