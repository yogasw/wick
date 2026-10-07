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

// backgroundRecheckTimeout bounds one stale re-check. It runs off the banner
// goroutine, so a slow answer only delays the next repaint of the set.
const backgroundRecheckTimeout = 10 * time.Second

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
	if s.subAgentStatusHidden() {
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
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil {
		s.mu.Unlock()
		return
	}
	t.bgAgents = append([]agentchannels.DetachedSurvivor(nil), active...)
	t.bgCheckedAt = time.Now()
	wasShowing := t.bgTicker != nil
	if len(active) == 0 {
		t.stopBackgroundBanner()
	}
	running := t.running
	channelID, threadTS := t.channelID, t.threadTS
	s.mu.Unlock()

	switch {
	case len(active) > 0:
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
	if s.subAgentStatusHidden() {
		return
	}
	s.mu.Lock()
	t := s.turns[sessionKey]
	if t == nil || t.running || len(t.bgAgents) == 0 {
		s.mu.Unlock()
		return
	}
	fresh := t.bgTicker == nil
	var stop chan struct{}
	var ticker *time.Ticker
	if fresh {
		ticker = time.NewTicker(statusAnimInterval)
		stop = make(chan struct{})
		t.bgTicker, t.bgStop = ticker, stop
	}
	channelID, threadTS := t.channelID, t.threadTS
	agents := append([]agentchannels.DetachedSurvivor(nil), t.bgAgents...)
	dot := t.bgDot
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
				if stillCurrent {
					cur.bgDot++
					dot = cur.bgDot
					agents = append(agents, cur.bgAgents...)
					// Same idea as the label age-out: a set nothing has
					// confirmed for a while may name a child that died
					// without closing its row, so ask again.
					if recheck != nil && !cur.bgRechecking && time.Since(cur.bgCheckedAt) > staleActivityAfter {
						cur.bgRechecking = true
						doRecheck = true
					}
				}
				s.mu.Unlock()
				if !stillCurrent {
					return
				}
				s.paintBackgroundBanner(channelID, threadTS, agents, dot)
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
// rotates one line per sub-agent.
func (s *Channel) paintBackgroundBanner(channelID, threadTS string, agents []agentchannels.DetachedSurvivor, dot int) {
	if channelID == "" || len(agents) == 0 {
		return
	}
	lines := make([]string, 0, len(agents))
	for _, a := range agents {
		lines = append(lines, backgroundName(a)+" is working")
	}
	s.setAssistantStatusWithLoading(channelID, threadTS, backgroundBannerText(agents)+strings.Repeat(".", dot%4), bubbleLoadingMessages(lines))
}

// backgroundBannerText is the banner's footer without its dots:
// "wick-fixer is working", or "2 sub-agents working: wick-fixer, wick-deployer".
func backgroundBannerText(agents []agentchannels.DetachedSurvivor) string {
	if len(agents) == 1 {
		return backgroundName(agents[0]) + " is working"
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, backgroundName(a))
	}
	return fmt.Sprintf("%d sub-agents working: %s", len(agents), strings.Join(names, ", "))
}

// backgroundName is the handle a human addresses the sub-agent by, falling
// back to its role.
func backgroundName(a agentchannels.DetachedSurvivor) string {
	switch {
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
