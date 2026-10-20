package slack

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Listen mode: the advanced 🤖 auto-reply.
//
// A listening thread passes every plain reply (no @mention) to the agent,
// but — unlike the old switch — not at once and not blindly:
//
//   - wick holds the reply for the agent's delay. If its author adds more,
//     the wait restarts; if someone else (a person or another bot) answers
//     in the meantime, the agent either reviews the exchange and adds only
//     what is missing (on_team_reply: review) or stays out (skip);
//   - the agent gets the thread's messages since it last looked plus a
//     [listen mode] note carrying its LISTEN.md instructions, and answers
//     [silent] when the reply is not for it;
//   - stopping never depends on the model: a bare "stop" reply, removing
//     the 🤖, or clicking the bot's own 🤖 turns the thread off and the bot
//     takes its 🤖 down.
//
// What shapes it is LISTEN.md: a wick-wide default (agents root, else the
// built-in one below) and the agent's own in its project folder, plus what
// the "listen" request set on the thread. See resolveListen.

// defaultListenDoc is used when the agents root has no LISTEN.md.
const defaultListenDoc = `---
delay: 15s
scope: thread
on_team_reply: review
---
Answer only when the message is a question or request that is really meant for you and fits your role.
Stay out ([silent]) when: a person or another bot already answered it fully, the client is talking to someone on our team, it is internal chatter, or you are unsure.
A thank-you, "ok" or similar acknowledgement meant for you: react with an emoji ([react :pray:], [react :+1:]…), no text.
When another answer falls short, add ONLY the missing part; do not repeat it.
Write like a teammate in chat: short, warm and natural, no stiff or formal phrasing, no headers for a one-line answer.
Never answer a client in an inquiry/main thread except through your agent's checkpoint flow.`

const (
	listenScopeThread  = "thread"
	listenScopeChannel = "channel"
	listenScopeAll     = "all"

	listenReplyReview = "review"
	listenReplySkip   = "skip"

	// listenMaxDelay caps any delay: a typo of "60m" must not park a reply
	// for an hour.
	listenMaxDelay = 15 * time.Minute
)

// listenSettings is the effective listen configuration of one thread.
type listenSettings struct {
	Delay        time.Duration
	Scope        string
	OnTeamReply  string
	Instructions string
}

// parseListenDoc splits a LISTEN.md into its frontmatter keys and body.
// A document without a leading "---" block is all body.
func parseListenDoc(doc string) (map[string]string, string) {
	keys := map[string]string{}
	doc = strings.ReplaceAll(doc, "\r\n", "\n")
	trimmed := strings.TrimLeft(doc, "\n \t")
	if !strings.HasPrefix(trimmed, "---\n") {
		return keys, strings.TrimSpace(doc)
	}
	rest := trimmed[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return keys, strings.TrimSpace(doc)
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" {
			keys[k] = v
		}
	}
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "-") // tolerate "----"
	return keys, strings.TrimSpace(body)
}

// parseListenDelay reads "60s", "2m", "90" (seconds) or "0".
func parseListenDelay(v string) (time.Duration, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil {
		v = strconv.Itoa(n) + "s"
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, false
	}
	if d > listenMaxDelay {
		d = listenMaxDelay
	}
	return d, true
}

// applyListenKeys lays one document's frontmatter over st.
func applyListenKeys(st *listenSettings, keys map[string]string) {
	if d, ok := parseListenDelay(keys["delay"]); ok {
		st.Delay = d
	}
	switch strings.ToLower(keys["scope"]) {
	case listenScopeThread, listenScopeChannel, listenScopeAll:
		st.Scope = strings.ToLower(keys["scope"])
	}
	switch strings.ToLower(keys["on_team_reply"]) {
	case listenReplyReview, listenReplySkip:
		st.OnTeamReply = strings.ToLower(keys["on_team_reply"])
	}
}

// resolveListen builds a thread's settings: the wick default (the file at
// the agents root, else the built-in one), then the agent's LISTEN.md —
// whose keys win one by one and whose body is added after the default's,
// or replaces it with replace_default: true — then what the thread's
// "listen" request set.
func resolveListen(wickDoc, agentDoc string, lt agentchannels.ListenThread) listenSettings {
	if strings.TrimSpace(wickDoc) == "" {
		wickDoc = defaultListenDoc
	}
	st := listenSettings{Scope: listenScopeThread, OnTeamReply: listenReplyReview}
	baseKeys, baseBody := parseListenDoc(wickDoc)
	applyListenKeys(&st, baseKeys)
	bodies := []string{baseBody}
	if strings.TrimSpace(agentDoc) != "" {
		keys, body := parseListenDoc(agentDoc)
		applyListenKeys(&st, keys)
		if b, _ := strconv.ParseBool(keys["replace_default"]); b {
			bodies = nil
		}
		bodies = append(bodies, body)
	}
	if p := strings.TrimSpace(lt.Prompt); p != "" {
		bodies = append(bodies, "For this thread: "+p)
	}
	if lt.DelaySec != nil {
		d := time.Duration(*lt.DelaySec) * time.Second
		if d < 0 {
			d = 0
		}
		if d > listenMaxDelay {
			d = listenMaxDelay
		}
		st.Delay = d
	}
	var kept []string
	for _, b := range bodies {
		if b = strings.TrimSpace(b); b != "" {
			kept = append(kept, b)
		}
	}
	st.Instructions = strings.Join(kept, "\n\n")
	return st
}

// BuiltinListenDoc is the default used while the agents root has no
// LISTEN.md of its own.
const BuiltinListenDoc = defaultListenDoc

// ListenEffective is what a thread of an agent gets before any "listen"
// request of its own: the wick default, then the agent's LISTEN.md.
type ListenEffective struct {
	DelaySec     int    `json:"delay_sec"`
	Scope        string `json:"scope"`
	OnTeamReply  string `json:"on_team_reply"`
	Instructions string `json:"instructions"`
}

// EffectiveListen resolves wickDoc (empty = the built-in default) and
// agentDoc the way a thread does, for Settings to show.
func EffectiveListen(wickDoc, agentDoc string) ListenEffective {
	st := resolveListen(wickDoc, agentDoc, agentchannels.ListenThread{})
	return ListenEffective{DelaySec: int(st.Delay / time.Second), Scope: st.Scope, OnTeamReply: st.OnTeamReply, Instructions: st.Instructions}
}

// listenStore is the session checker's listen half, when it has one.
func (s *Channel) listenStore() (agentchannels.ListenStore, bool) {
	if s.sessions == nil {
		return nil, false
	}
	ls, ok := s.sessions.(agentchannels.ListenStore)
	return ls, ok
}

// listenSettingsFor resolves sessionID's settings; the defaults when the
// checker keeps no listen state.
func (s *Channel) listenSettingsFor(sessionID string) listenSettings {
	ls, ok := s.listenStore()
	if !ok {
		return resolveListen("", "", agentchannels.ListenThread{})
	}
	wickDoc, agentDoc := ls.ListenDocs(sessionID)
	return resolveListen(wickDoc, agentDoc, ls.ListenThread(sessionID))
}

// listenManual reports whether channelID is one where a new thread does not
// start listening on its own, and where a channel/all scope never applies:
// a client-facing channel a person must switch on thread by thread.
func listenManual(cfg agentconfig.SlackChannelConfig, channelID string) bool {
	return pickerHas(cfg.ListenManualChannels, channelID)
}

// listening reports whether a reply in sessionID's thread (in channelID)
// goes to the agent without a mention: the thread was switched on, or the
// agent's scope covers it (a channel it listens to, or every thread it was
// ever called into) and nobody stopped this thread.
func (s *Channel) listening(sessionID, channelID string) bool {
	if s.autoReplyOn(sessionID) {
		return true
	}
	ls, ok := s.listenStore()
	if !ok || !s.sessionOnDisk(sessionID) {
		return false
	}
	cfg := s.snapshot()
	if !cfg.ReactionTriggerEnabled || !reactionChannelAllowed(cfg, channelID) || listenManual(cfg, channelID) {
		return false
	}
	if ls.ListenThread(sessionID).Stopped {
		return false
	}
	switch s.listenSettingsFor(sessionID).Scope {
	case listenScopeAll:
		return true
	case listenScopeChannel:
		return ls.ListenChannel(s.sessionPrefixSnapshot(), channelID)
	}
	return false
}

// stopWords are the replies that switch a listening thread off without the
// model: wick acts on them itself, so an agent that ignores instructions
// cannot keep talking.
var stopWords = map[string]bool{
	"stop": true, "berhenti": true, "stop listen": true, "stop listening": true,
	"berhenti dengar": true, "berhenti mendengarkan": true, "jangan jawab": true,
	"jangan dijawab": true, "unlisten": true,
}

// isStopCommand reports whether text, once mentions and punctuation are
// stripped, is just a stop word.
func isStopCommand(text string) bool {
	t := strings.ToLower(text)
	for {
		i := strings.Index(t, "<@")
		if i < 0 {
			break
		}
		j := strings.Index(t[i:], ">")
		if j < 0 {
			break
		}
		t = t[:i] + " " + t[i+j+1:]
	}
	t = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, t)
	t = strings.Join(strings.Fields(t), " ")
	return stopWords[t]
}

// ListenOn switches threadTS's listening on: the auto-reply flag, what the
// request set for the thread, the bot's 🤖 on the parent, and — for a
// channel scope — the channel as a whole.
func (s *Channel) ListenOn(sessionID, channelID, threadTS, prompt string, delaySec *int) listenSettings {
	s.setAutoReply(sessionID, true)
	if ls, ok := s.listenStore(); ok {
		ls.SetListenThread(sessionID, agentchannels.ListenThread{Prompt: strings.TrimSpace(prompt), DelaySec: delaySec})
	}
	s.persistThreadBinding(sessionID, channelID, threadTS)
	s.setReaction(reactionTrigger, channelID, threadTS, "")
	st := s.listenSettingsFor(sessionID)
	if st.Scope == listenScopeChannel {
		if ls, ok := s.listenStore(); ok {
			ls.SetListenChannel(s.sessionPrefixSnapshot(), channelID, true)
		}
	}
	log.Info().Str("channel", "slack").Str("slack_channel", channelID).Str("thread_ts", threadTS).
		Str("session", sessionID).Dur("delay", st.Delay).Str("scope", st.Scope).Msg("listen ON")
	return st
}

// ListenOff switches threadTS's listening off and takes the bot's 🤖 down,
// so nothing on the thread still reads as on. wholeChannel also drops the
// channel from a channel scope. by is who or what stopped it, for the log.
func (s *Channel) ListenOff(sessionID, channelID, threadTS, by string, wholeChannel bool) {
	s.setAutoReply(sessionID, false)
	s.cancelListenWait(sessionID)
	if ls, ok := s.listenStore(); ok {
		ls.SetListenThread(sessionID, agentchannels.ListenThread{Stopped: true})
		if wholeChannel {
			ls.SetListenChannel(s.sessionPrefixSnapshot(), channelID, false)
		}
	}
	s.removeOwnTrigger(channelID, threadTS)
	log.Info().Str("channel", "slack").Str("slack_channel", channelID).Str("thread_ts", threadTS).
		Str("session", sessionID).Str("by", by).Bool("whole_channel", wholeChannel).Msg("listen OFF")
}

// removeOwnTrigger takes the bot's own 🤖 off a parent. "no_reaction" (the
// bot never put one there) is the expected answer often enough not to log.
func (s *Channel) removeOwnTrigger(channelID, threadTS string) {
	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil || channelID == "" || threadTS == "" {
		return
	}
	err := api.RemoveReaction(reactionTrigger, slackgo.ItemRef{Channel: channelID, Timestamp: threadTS})
	if err != nil && !strings.Contains(err.Error(), "no_reaction") {
		log.Warn().Str("channel", "slack").Str("slack_channel", channelID).Str("thread_ts", threadTS).
			Err(err).Msg("listen: could not remove the bot's 🤖")
	}
}

// ListenRequest / ListenResult are the wick_slack_listen tool's input and
// answer, resolved against the session's thread binding.
type ListenRequest = agentchannels.ListenRequest
type ListenResult = agentchannels.ListenResult

// ThreadListen serves wick_slack_listen for a session this instance owns:
// on, off or status of the thread the session is bound to.
func (s *Channel) ThreadListen(sessionID string, req ListenRequest) (ListenResult, error) {
	if s.sessions == nil {
		return ListenResult{}, fmt.Errorf("slack channel has no session store")
	}
	b, ok := s.sessions.ThreadBinding(sessionID)
	if !ok || b.Channel != "slack" || b.ChatID == "" || b.ThreadID == "" {
		return ListenResult{}, fmt.Errorf("this session is not bound to a Slack thread")
	}
	if strings.HasPrefix(b.ChatID, "D") {
		return ListenResult{}, fmt.Errorf("a direct message always gets an answer; listen mode is for channel threads")
	}
	switch strings.ToLower(req.Action) {
	case "on":
		s.ListenOn(sessionID, b.ChatID, b.ThreadID, req.Instructions, req.DelaySec)
	case "off":
		s.ListenOff(sessionID, b.ChatID, b.ThreadID, "tool", req.WholeChannel)
	case "", "status":
	default:
		return ListenResult{}, fmt.Errorf("action must be on, off or status")
	}
	st := s.listenSettingsFor(sessionID)
	var threadPrompt string
	if ls, ok := s.listenStore(); ok {
		threadPrompt = ls.ListenThread(sessionID).Prompt
	}
	return ListenResult{
		ThreadPrompt: threadPrompt,
		Listening:    s.listening(sessionID, b.ChatID),
		ChannelID:    b.ChatID,
		ThreadTS:     b.ThreadID,
		DelaySec:     int(st.Delay / time.Second),
		Scope:        st.Scope,
		OnTeamReply:  st.OnTeamReply,
		Instructions: st.Instructions,
	}, nil
}

// listenWait is one thread's held reply.
type listenWait struct {
	timer         *time.Timer
	ev            *slackevents.MessageEvent
	files         []slackgo.File
	author        string
	othersReplied bool
	st            listenSettings
	started       time.Time
}

// queueListenMessage takes a plain reply in a listening thread: it goes to
// the agent after the delay, or at once when there is none.
func (s *Channel) queueListenMessage(ctx context.Context, sessionID string, ev *slackevents.MessageEvent, files []slackgo.File) {
	st := s.listenSettingsFor(sessionID)
	if st.Delay <= 0 {
		s.dispatchListen(sessionID, ev, files, st, 0, false)
		return
	}
	s.mu.Lock()
	if s.listenWaits == nil {
		s.listenWaits = map[string]*listenWait{}
	}
	w := s.listenWaits[sessionID]
	switch {
	case w == nil:
		w = &listenWait{ev: ev, files: files, author: ev.User, st: st, started: time.Now()}
		w.timer = time.AfterFunc(st.Delay, func() { s.fireListenWait(sessionID, w) })
		s.listenWaits[sessionID] = w
	case w.author == ev.User:
		// The asker is still typing it out: wait for the whole question.
		w.ev = ev
		w.files = append(w.files, files...)
		w.timer.Reset(w.st.Delay)
	default:
		s.noteOtherLocked(sessionID, w)
	}
	s.mu.Unlock()
	_ = ctx
}

// noteListenOther records that someone other than the asker (a person or
// another bot) spoke while the thread's reply was held.
func (s *Channel) noteListenOther(sessionID, author string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.listenWaits[sessionID]; w != nil && w.author != author {
		s.noteOtherLocked(sessionID, w)
	}
}

// noteOtherLocked marks w answered; with on_team_reply: skip the held reply
// is dropped at once, without a turn. s.mu is held.
func (s *Channel) noteOtherLocked(sessionID string, w *listenWait) {
	w.othersReplied = true
	if w.st.OnTeamReply == listenReplySkip {
		w.timer.Stop()
		delete(s.listenWaits, sessionID)
		log.Info().Str("channel", "slack").Str("session", sessionID).
			Msg("listen: someone else answered during the delay; skipped (on_team_reply: skip)")
	}
}

// cancelListenWait drops a held reply: the thread stopped listening, or a
// mention is answering now and already sees it.
func (s *Channel) cancelListenWait(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.listenWaits[sessionID]; w != nil {
		w.timer.Stop()
		delete(s.listenWaits, sessionID)
	}
}

// fireListenWait hands a held reply to the agent once its delay ran out.
func (s *Channel) fireListenWait(sessionID string, w *listenWait) {
	s.mu.Lock()
	if s.listenWaits[sessionID] != w {
		s.mu.Unlock()
		return
	}
	delete(s.listenWaits, sessionID)
	others := w.othersReplied
	s.mu.Unlock()
	if others && w.st.OnTeamReply == listenReplySkip {
		return
	}
	s.dispatchListen(sessionID, w.ev, w.files, w.st, time.Since(w.started), others)
}

// listenTurn travels on the context of a turn listen mode started.
type listenTurn struct {
	st            listenSettings
	waited        time.Duration
	othersReplied bool
	// threadPrompt is this thread's own "listen" instructions.
	threadPrompt string
	// docsChanged: LISTEN.md differs from what the process was spawned
	// with, so docInstructions go along in the note.
	docsChanged     bool
	docInstructions string
}

type listenTurnKey struct{}

func listenTurnFrom(ctx context.Context) (listenTurn, bool) {
	lt, ok := ctx.Value(listenTurnKey{}).(listenTurn)
	return lt, ok
}

// dispatchListen runs the turn for a listening thread's reply.
func (s *Channel) dispatchListen(sessionID string, ev *slackevents.MessageEvent, files []slackgo.File, st listenSettings, waited time.Duration, others bool) {
	if s.listenDispatchHook != nil {
		s.listenDispatchHook(sessionID, ev, waited, others)
		return
	}
	log.Info().Str("channel", "slack").Str("session", sessionID).Dur("waited", waited).
		Bool("others_replied", others).Msg("listen: passing the reply to the agent")
	lt := listenTurn{st: st, waited: waited, othersReplied: others}
	if ls, ok := s.listenStore(); ok {
		lt.threadPrompt = ls.ListenThread(sessionID).Prompt
		wickDoc, agentDoc := ls.ListenDocs(sessionID)
		lt.docInstructions = resolveListen(wickDoc, agentDoc, agentchannels.ListenThread{}).Instructions
		lt.docsChanged = s.listenDocsChanged(sessionID, lt.docInstructions)
	}
	ctx := context.WithValue(context.Background(), listenTurnKey{}, lt)
	s.handleMessage(ctx, ev, files)
}

// listenRules is what every listening agent is told once, in its system
// prompt (ListenPrompt); a listening turn itself only carries listenNote.
const listenRules = `A message that opens with "[listen mode]" is a reply in a Slack thread you listen to: it did not mention you, and wick may have held it for a while first (the thread block that follows also shows what was said during the wait). Answer only if it is a question or request meant for you; otherwise reply exactly [silent]. When the note says someone else replied meanwhile, read that first and add only what is still missing. A thank-you or a plain acknowledgement meant for you gets a reaction instead of words: reply exactly [react :pray:] (any Slack emoji name). A reply that is just "stop" is handled by wick. A message without the marker (a mention, a DM) is answered as usual.`

// ListenPrompt is the "Slack listen mode" section of sessionID's system
// prompt: the rules and its LISTEN.md instructions. The instructions are
// remembered so a turn after a LISTEN.md edit carries the new ones itself.
func (s *Channel) ListenPrompt(sessionID string) string {
	ls, ok := s.listenStore()
	if !ok {
		return ""
	}
	wickDoc, agentDoc := ls.ListenDocs(sessionID)
	instr := resolveListen(wickDoc, agentDoc, agentchannels.ListenThread{}).Instructions
	s.listenPromptMu.Lock()
	if s.listenPromptSeen == nil {
		s.listenPromptSeen = map[string]string{}
	}
	s.listenPromptSeen[sessionID] = instr
	s.listenPromptMu.Unlock()
	var b strings.Builder
	b.WriteString("## Slack listen mode\n\n")
	b.WriteString(listenRules)
	if instr != "" {
		b.WriteString("\n\nListen instructions (LISTEN.md):\n")
		b.WriteString(instr)
	}
	return b.String()
}

// listenDocsChanged reports whether sessionID's LISTEN.md instructions
// differ from the ones its running process got in its system prompt, and
// records the current ones as delivered.
func (s *Channel) listenDocsChanged(sessionID, instr string) bool {
	s.listenPromptMu.Lock()
	defer s.listenPromptMu.Unlock()
	if s.listenPromptSeen == nil {
		s.listenPromptSeen = map[string]string{}
	}
	prev, known := s.listenPromptSeen[sessionID]
	s.listenPromptSeen[sessionID] = instr
	// Unknown: no process yet, so the spawn this turn starts reads the
	// current file in its system prompt.
	return known && prev != instr
}

// listenNote is the one-line [listen mode] marker a listening turn opens
// with; the rules sit in the system prompt. The thread's own instructions
// ride along, and so do the LISTEN.md ones when they changed since spawn.
func listenNote(lt listenTurn) string {
	var b strings.Builder
	b.WriteString("[listen mode")
	if lt.waited > 0 {
		fmt.Fprintf(&b, " · held %s", lt.waited.Round(time.Second))
	}
	if lt.othersReplied {
		b.WriteString(" · someone else replied meanwhile")
	}
	b.WriteString("] Not a mention: answer only if it is meant for you, else [silent]; a thank-you gets [react :pray:].")
	if lt.threadPrompt != "" {
		b.WriteString("\nFor this thread: ")
		b.WriteString(lt.threadPrompt)
	}
	if lt.docsChanged && lt.docInstructions != "" {
		b.WriteString("\nLISTEN.md changed, these replace the ones in your system prompt:\n")
		b.WriteString(lt.docInstructions)
	}
	return b.String()
}

// slackTSNow is a Slack timestamp for the current moment: the upper bound
// of a listening turn's catch-up, which also covers what was said after
// the reply that woke it.
func slackTSNow() string {
	now := time.Now()
	return fmt.Sprintf("%d.%06d", now.Unix(), now.Nanosecond()/1000)
}
