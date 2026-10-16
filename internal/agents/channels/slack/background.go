package slack

import (
	"context"
	"fmt"
	"strings"
	"time"

	slackgo "github.com/slack-go/slack"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// Background sub-agents in a thread.
//
// A leader that fires a sub-agent in the background usually ends its turn
// right after, and Slack drops the assistant banner with the reply. The web UI
// keeps spinning — its sub-agent rail reads the delegation rows — but the
// thread reads as finished while work is still going. So the channel keeps a
// banner of its own for the background set, fed from those same rows (see
// delegation.ActiveBackground), and posts one short message when each one
// starts so the human gets a notification.
//
// Teammates a Team agent is waiting on (working team tasks) ride in the same
// set, marked Teammate. They are not sub-agents, so HideSubAgentStatus does
// not hide them; the agent's own "Show teammates in channels" does (Hidden),
// and then the thread sees only the generic working state.

// backgroundRecheckTimeout bounds one stale re-check. It runs off the banner
// goroutine, so a slow answer only delays the next repaint of the set.
const backgroundRecheckTimeout = 10 * time.Second

// teammateKeepAlive is how often a banner showing only teammates is
// written again. Teammates can work for many minutes, and a status write
// every statusAnimInterval per thread for that long would spend Slack's
// rate limit on dots; Slack keeps a status about two minutes, so one
// write well inside that keeps it up. A change of the set repaints at
// once (OnBackgroundAgents). A test sets Channel.bgKeepAlive instead.
const teammateKeepAlive = 45 * time.Second

// keepAliveLocked is the teammate keep-alive of this channel. Caller
// holds mu.
func (s *Channel) keepAliveLocked() time.Duration {
	if s.bgKeepAlive > 0 {
		return s.bgKeepAlive
	}
	return teammateKeepAlive
}

// bgTickLocked is how often the background banner ticks. Caller holds mu.
func (s *Channel) bgTickLocked() time.Duration {
	if s.bgTick > 0 {
		return s.bgTick
	}
	return statusAnimInterval
}

// SetBackgroundRecheck satisfies channels.BackgroundRecheckSetter.
func (s *Channel) SetBackgroundRecheck(fn agentchannels.BackgroundRecheckFn) {
	s.cfgMu.Lock()
	s.bgRecheck = fn
	s.cfgMu.Unlock()
}

func (s *Channel) subAgentStatusHidden() bool {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfg.HideSubAgentStatus
}

// OnBackgroundStart satisfies channels.BackgroundWorkReceiver: one short
// message per background delegation, so a human watching the thread is told
// work was fired off before the leader goes quiet. Foreground delegations
// never reach here — the leader's live turn already shows them.
func (s *Channel) OnBackgroundStart(sessionKey string, agent agentchannels.DetachedSurvivor, task string, queued bool) {
	if agent.Teammate && agent.Hidden || !agent.Teammate && s.subAgentStatusHidden() {
		return
	}
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil || t.channelID == "" {
		// Not a thread of this instance; another channel or the web UI owns it.
		s.mu.Unlock()
		return
	}
	channelID, threadTS := t.channelID, t.threadTS
	s.mu.Unlock()

	s.cfgMu.Lock()
	api := s.api
	s.cfgMu.Unlock()
	if api == nil {
		return
	}
	// The brief is the leader's raw text: its source tags and options line
	// become plain text first (an unknown handle is dropped). Names are left
	// unescaped here because the post below escapes the whole body.
	task = agentchannels.RewriteSourceTags(sessionKey, agentchannels.RewriteOptionsLine(task), nil)
	body := backgroundStartText(agent, task, queued)
	s.withBackoff(func() error {
		// escape=true: the task is the leader's own words and may quote a
		// <@U…> mention, which would otherwise ping that person again.
		_, _, err := api.PostMessage(
			channelID,
			slackgo.MsgOptionText(body, true),
			slackgo.MsgOptionTS(threadTS),
		)
		return err
	})
}

// backgroundStartText renders the start ping: who, and what it was asked.
func backgroundStartText(agent agentchannels.DetachedSurvivor, task string, queued bool) string {
	icon, verb := "🔧", "started"
	if agent.Teammate {
		icon = "🤝"
	}
	if queued {
		icon, verb = "⏳", "queued"
	}
	task = strings.Join(strings.Fields(task), " ")
	if task == "" {
		return fmt.Sprintf("%s %s %s", icon, backgroundName(agent), verb)
	}
	return fmt.Sprintf("%s %s %s: %s", icon, backgroundName(agent), verb, task)
}

// OnBackgroundAgents satisfies channels.BackgroundWorkReceiver. It records the
// current set and, when the leader's own turn is not running, shows, repaints
// or clears the banner to match it.
func (s *Channel) OnBackgroundAgents(sessionKey string, active []agentchannels.DetachedSurvivor) {
	hideSubs := s.subAgentStatusHidden()
	s.mu.Lock()
	t := s.turns[sessionKey]
	s.mu.Unlock()
	if t == nil && len(bannerAgents(active, hideSubs)) > 0 {
		// No turn here (a restart, a handover, a turn already handed
		// back): rebind from the session's stored thread so the banner
		// still shows. Not ours or unbound = nothing to show.
		t = s.restoreBackgroundTurn(sessionKey)
	}
	s.mu.Lock()
	if t = s.turns[sessionKey]; t == nil {
		s.mu.Unlock()
		return
	}
	t.bgAgents = append([]agentchannels.DetachedSurvivor(nil), active...)
	t.bgCheckedAt = time.Now()
	wasShowing := t.bgTicker != nil
	// The set is kept whole (a DM's turn stays while any of it works), but
	// the banner only follows what it may show.
	shown := len(bannerAgents(active, hideSubs)) > 0
	if !shown {
		t.stopBackgroundBanner()
	}
	running := t.running
	channelID, threadTS := t.channelID, t.threadTS
	s.mu.Unlock()

	switch {
	case shown:
		s.startBackgroundBanner(sessionKey)
	case wasShowing && !running:
		// The last one finished and no turn owns the banner: clear it.
		s.setAssistantStatus(channelID, threadTS, "")
	}
}

// startBackgroundBanner shows the background banner for sessionKey and keeps
// it alive every statusAnimInterval, or repaints it now when it is already
// up. No-op while the leader's own turn is running — that turn's banner owns
// the thread — or when nothing is left in the background.
func (s *Channel) startBackgroundBanner(sessionKey string) {
	hideSubs := s.subAgentStatusHidden()
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil || t.running || len(bannerAgents(t.bgAgents, hideSubs)) == 0 {
		s.mu.Unlock()
		return
	}
	fresh := t.bgTicker == nil
	var stop chan struct{}
	var ticker *time.Ticker
	if fresh {
		ticker = time.NewTicker(s.bgTickLocked())
		stop = make(chan struct{})
		t.bgTicker, t.bgStop = ticker, stop
	}
	channelID, threadTS := t.channelID, t.threadTS
	agents := bannerAgents(t.bgAgents, hideSubs)
	dot := t.bgDot
	t.bgPaintedAt = time.Now()
	s.mu.Unlock()

	s.paintBackgroundBanner(channelID, threadTS, agents, dot)
	if !fresh {
		return
	}

	go func() {
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s.cfgMu.Lock()
				recheck := s.bgRecheck
				s.cfgMu.Unlock()

				s.mu.Lock()
				cur := s.turns[sessionKey]
				stillCurrent := cur != nil && cur.bgStop == stop
				var agents []agentchannels.DetachedSurvivor
				var dot int
				var doRecheck bool
				paint := true
				if stillCurrent {
					agents = bannerAgents(cur.bgAgents, hideSubs)
					// Only teammates: no dots, a keep-alive write instead.
					if onlyTeammates(agents) && time.Since(cur.bgPaintedAt) < s.keepAliveLocked() {
						paint = false
					} else {
						cur.bgDot++
						cur.bgPaintedAt = time.Now()
					}
					dot = cur.bgDot
					// Same idea as the label age-out: a set nothing has
					// confirmed for a while may name a child that died
					// without closing its row, so ask again.
					if recheck != nil && !cur.bgRechecking && time.Since(cur.bgCheckedAt) > staleActivityAfter {
						cur.bgRechecking = true
						doRecheck = true
					}
				}
				s.mu.Unlock()
				if !stillCurrent || len(agents) == 0 {
					return
				}
				if paint {
					s.paintBackgroundBanner(channelID, threadTS, agents, dot)
				}
				if doRecheck {
					go s.recheckBackground(sessionKey, recheck)
				}
			}
		}
	}()
}

// recheckBackground asks for the live background set again and applies the
// answer. A failed probe keeps the current set and waits another interval.
func (s *Channel) recheckBackground(sessionKey string, recheck agentchannels.BackgroundRecheckFn) {
	ctx, cancel := context.WithTimeout(context.Background(), backgroundRecheckTimeout)
	defer cancel()
	active, ok := recheck(ctx, sessionKey)

	s.mu.Lock()
	if cur := s.turns[sessionKey]; cur != nil {
		cur.bgRechecking = false
		if !ok {
			cur.bgCheckedAt = time.Now()
		}
	}
	s.mu.Unlock()
	if ok {
		s.OnBackgroundAgents(sessionKey, active)
	}
}

// stopBackgroundBanner halts the background keep-alive, if any. Caller holds
// Channel.mu. It does not clear the Slack status — whoever stops it either
// paints its own banner next or clears it.
func (t *turn) stopBackgroundBanner() {
	if t == nil || t.bgTicker == nil {
		return
	}
	t.bgTicker.Stop()
	close(t.bgStop)
	t.bgTicker = nil
	t.bgStop = nil
}

// paintBackgroundBanner writes the background banner: the footer names who is
// working with the same animated dots as a turn's banner, and the bubble
// rotates one line per sub-agent or named teammate.
func (s *Channel) paintBackgroundBanner(channelID, threadTS string, agents []agentchannels.DetachedSurvivor, dot int) {
	if channelID == "" || len(agents) == 0 {
		return
	}
	var lines []string
	for _, a := range agents {
		if !a.Hidden {
			lines = append(lines, backgroundName(a)+" is working")
		}
	}
	if len(lines) == 0 {
		lines = []string{footerWorking}
	}
	if onlyTeammates(agents) {
		dot = 0 // repainted on teammateKeepAlive: dots would freeze mid-way
	}
	s.setAssistantStatusWithLoading(channelID, threadTS, backgroundFooter(agents, dot), bubbleLoadingMessages(lines))
}

// bannerAgents is the part of the background set the banner shows: every
// teammate (named or, when Hidden, as the generic working state), and the
// sub-agents unless their status is hidden.
func bannerAgents(agents []agentchannels.DetachedSurvivor, hideSubs bool) []agentchannels.DetachedSurvivor {
	out := make([]agentchannels.DetachedSurvivor, 0, len(agents))
	for _, a := range agents {
		if a.Teammate || !hideSubs {
			out = append(out, a)
		}
	}
	return out
}

// onlyTeammates reports whether every entry of a non-empty set is a
// teammate (no sub-agent, whose banner animates).
func onlyTeammates(agents []agentchannels.DetachedSurvivor) bool {
	for _, a := range agents {
		if !a.Teammate {
			return false
		}
	}
	return len(agents) > 0
}

// backgroundBannerText is the banner's footer without its dots:
// "wick-fixer is working", "2 sub-agents working: wick-fixer, wick-deployer",
// "is waiting on @halodev, @qa (1/3 replied)" (Slack puts the app's name in
// front), or, with only hidden teammates, the plain "Working" of a turn.
func backgroundBannerText(agents []agentchannels.DetachedSurvivor) string {
	return backgroundFooter(agents, 0)
}

// backgroundFooter renders the footer with dot animated dots, placed after
// the names so the "(1/3 replied)" count stays put.
func backgroundFooter(agents []agentchannels.DetachedSurvivor, dot int) string {
	dots := strings.Repeat(".", dot%4)
	var subs, mates []string
	replied, of := 0, 0
	for _, a := range agents {
		switch {
		case !a.Teammate:
			subs = append(subs, backgroundName(a))
		case !a.Hidden:
			mates = append(mates, backgroundName(a))
			replied, of = a.Replied, a.Of
		}
	}
	var subText string
	switch len(subs) {
	case 0:
	case 1:
		subText = subs[0] + " is working"
	default:
		subText = fmt.Sprintf("%d sub-agents working: %s", len(subs), strings.Join(subs, ", "))
	}
	if len(mates) == 0 {
		if subText == "" {
			// Only teammates the channel must not name: a normal turn's look.
			return footerWorking + dots
		}
		return subText + dots
	}
	text := "is waiting on " + strings.Join(mates, ", ") + dots
	if of > 0 {
		text += fmt.Sprintf(" (%d/%d replied)", replied, of)
	}
	if subText != "" {
		text += " · " + subText
	}
	return text
}

// backgroundName is the handle a human addresses the sub-agent by, falling
// back to its role. A teammate is addressed as @handle.
func backgroundName(a agentchannels.DetachedSurvivor) string {
	switch {
	case a.Teammate && a.Handle != "":
		return "@" + a.Handle
	case a.Handle != "":
		return a.Handle
	case a.ProfileKey != "":
		return a.ProfileKey
	}
	return "sub-agent"
}

// hasBackgroundAgents reports whether sessionKey's turn still tracks
// sub-agents working in the background.
func (s *Channel) hasBackgroundAgents(sessionKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turns[sessionKey]
	return t != nil && len(t.bgAgents) > 0
}

// restoreBackgroundTurn rebinds sessionKey's thread for the background
// banner alone. ensureTurn restores a turn as running (it is made for a
// reply on its way); here no turn runs, so the banner may paint.
func (s *Channel) restoreBackgroundTurn(sessionKey string) *turn {
	s.mu.Lock()
	had := s.turns[sessionKey] != nil
	s.mu.Unlock()
	t := s.ensureTurn(sessionKey)
	if t == nil || had {
		return t
	}
	s.mu.Lock()
	if s.turns[sessionKey] == t && t.statusTicker == nil {
		t.running = false
	}
	s.mu.Unlock()
	return t
}
