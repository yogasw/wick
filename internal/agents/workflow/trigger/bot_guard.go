package trigger

import (
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// Bot-to-bot loop guard for workflows. Channel messages posted by apps
// reach workflow triggers on purpose (intake forms file requests as bot
// messages), and the only skip upstream is "this bot's own post". That
// leaves a loop open when a workflow's reply still matches its own
// trigger and is posted by a DIFFERENT bot of the same host: with no
// pinned instance the run posts as whichever bot heard the event first,
// that post reaches the other bot as a fresh bot message, and the
// workflow runs again — forever, while the trigger keeps matching.
//
// The agent side already caps bot-triggered turns per thread (see
// channels/slack/bots.go); this is the same rule for workflow runs: at
// most botRunLimit runs started by a bot message per workflow per thread
// inside botRunWindow. Human messages and events without a thread key
// are never counted.
const (
	botRunLimit  = 5
	botRunWindow = 10 * time.Minute
	// botRunSweepAt bounds the map: past this many tracked threads, the
	// next call drops every entry whose window has fully passed.
	botRunSweepAt = 4096
)

type botRunGuard struct {
	mu   sync.Mutex
	runs map[string][]time.Time
}

// allow records one bot-triggered run for key at now and reports whether
// it fits the budget.
func (g *botRunGuard) allow(key string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.runs == nil {
		g.runs = map[string][]time.Time{}
	}
	if len(g.runs) >= botRunSweepAt {
		for k, at := range g.runs {
			if len(at) == 0 || now.Sub(at[len(at)-1]) >= botRunWindow {
				delete(g.runs, k)
			}
		}
	}
	kept := g.runs[key][:0]
	for _, at := range g.runs[key] {
		if now.Sub(at) < botRunWindow {
			kept = append(kept, at)
		}
	}
	if len(kept) >= botRunLimit {
		g.runs[key] = kept
		return false
	}
	g.runs[key] = append(kept, now)
	return true
}

// allowBotRun applies the bot loop guard to one candidate run of wfID.
// Only events flagged is_bot with a thread key are counted.
func (r *Router) allowBotRun(wfID string, evt workflow.Event) bool {
	if isBot, _ := evt.Payload["is_bot"].(bool); !isBot {
		return true
	}
	thread, _ := evt.Payload["thread"].(string)
	if thread == "" {
		return true
	}
	channelID, _ := evt.Payload["channel_id"].(string)
	now := time.Now()
	if r.clock != nil {
		now = r.clock()
	}
	if r.botRuns.allow(wfID+"|"+evt.Channel+"|"+channelID+"|"+thread, now) {
		return true
	}
	log.Warn().Str("component", "wf").Str("wf_id", wfID).
		Str("channel", evt.Channel).Str("channel_id", channelID).Str("thread", thread).
		Int("limit", botRunLimit).Dur("window", botRunWindow).
		Msg("dispatch: bot-triggered run budget spent for this thread — run skipped (bot loop guard)")
	return false
}
