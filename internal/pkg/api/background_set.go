package api

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// backgroundSets builds the background set a session's channels see: its
// background sub-agents (delegation rows) plus the teammates it is waiting
// on (working Team tasks it sent). Always computed whole from the two
// sources, never stored, so a restart rebuilds it from what they persist.
type backgroundSets struct {
	// subAgents reads the background sub-agents still live under a
	// session; recheck drops running rows whose process is gone.
	subAgents func(ctx context.Context, session string, recheck bool) ([]delegation.Survivor, error)
	// liveTasks lists every open Team task (nil = no Team).
	liveTasks func() []teamlink.LiveTask
	// progress counts a session's open batch: replied out of how many.
	progress func(session string) (replied, total int)
	// hidden: the session's agent has "Show teammates in channels" off.
	hidden func(session string) bool
	// dispatch hands the set to the channels.
	dispatch func(session string, set []agentchannels.DetachedSurvivor)
	// liveParents lists the sessions with live background sub-agents, for
	// the boot replay.
	liveParents func(ctx context.Context) ([]string, error)

	locks sync.Map // session → *sync.Mutex
}

// newBackgroundSets wires the sets over the delegation service, the Team
// hub and the channel registry.
func newBackgroundSets(svc *delegation.Service, hub func() *teamlink.Hub, reg *agentchannels.Registry, hidden func(string) bool) *backgroundSets {
	b := &backgroundSets{
		hidden:   hidden,
		dispatch: reg.DispatchBackgroundAgents,
	}
	if svc != nil {
		b.subAgents = func(ctx context.Context, session string, recheck bool) ([]delegation.Survivor, error) {
			if recheck {
				return svc.RecheckBackground(ctx, session)
			}
			return svc.ActiveBackground(ctx, session)
		}
		b.liveParents = func(ctx context.Context) ([]string, error) {
			m, err := svc.LiveBackgroundByParent(ctx)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(m))
			for p := range m {
				out = append(out, p)
			}
			return out, nil
		}
	}
	if hub != nil {
		b.liveTasks = func() []teamlink.LiveTask {
			if h := hub(); h != nil {
				return h.LiveTasks()
			}
			return nil
		}
		b.progress = func(session string) (int, int) {
			if h := hub(); h != nil {
				return h.BatchProgress(session)
			}
			return 0, 0
		}
	}
	return b
}

// compute returns session's whole background set: sub-agents first, then
// one entry per teammate handle still working on a task the session sent.
// A read error of the sub-agent rows is returned, so the caller keeps the
// set it has rather than clearing it on a hiccup.
func (b *backgroundSets) compute(ctx context.Context, session string, recheck bool) ([]agentchannels.DetachedSurvivor, error) {
	var out []agentchannels.DetachedSurvivor
	if b.subAgents != nil {
		subs, err := b.subAgents(ctx, session, recheck)
		if err != nil {
			return nil, err
		}
		out = channelSurvivors(subs)
	}
	return append(out, b.teammates(session)...), nil
}

// teammates is the teammate part of session's set: the handles of its
// working tasks, deduplicated (two tasks to one teammate = one entry). A
// question back to the session is not working: its own turn answers it.
func (b *backgroundSets) teammates(session string) []agentchannels.DetachedSurvivor {
	if b.liveTasks == nil {
		return nil
	}
	var handles []string
	seen := map[string]bool{}
	for _, t := range b.liveTasks() {
		if t.FromSession != session || !t.Working || t.ToHandle == "" || seen[t.ToHandle] {
			continue
		}
		seen[t.ToHandle] = true
		handles = append(handles, t.ToHandle)
	}
	if len(handles) == 0 {
		return nil
	}
	hidden := b.hidden != nil && b.hidden(session)
	replied, of := 0, 0
	if b.progress != nil {
		replied, of = b.progress(session)
	}
	out := make([]agentchannels.DetachedSurvivor, 0, len(handles))
	for _, h := range handles {
		out = append(out, agentchannels.DetachedSurvivor{Handle: h, Teammate: true, Hidden: hidden, Replied: replied, Of: of})
	}
	return out
}

// report recomputes session's set and hands it to the channels. Serialised
// per session, so two changes landing together cannot deliver the older
// snapshot last. A failed read keeps whatever the channels have.
func (b *backgroundSets) report(session string) {
	if session == "" || b.dispatch == nil {
		return
	}
	mu, _ := b.locks.LoadOrStore(session, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	set, err := b.compute(ctx, session, false)
	if err != nil {
		log.Warn().Err(err).Str("session", session).Msg("background set")
		return
	}
	b.dispatch(session, set)
}

// recheck is the channels' stale probe over the whole set.
func (b *backgroundSets) recheck() agentchannels.BackgroundRecheckFn {
	return func(ctx context.Context, session string) ([]agentchannels.DetachedSurvivor, bool) {
		set, err := b.compute(ctx, session, true)
		if err != nil {
			return nil, false
		}
		return set, true
	}
}

// waitingSessions lists every session with something in the background:
// a working Team task it sent, or a live background sub-agent. Sorted,
// each once.
func (b *backgroundSets) waitingSessions(ctx context.Context) []string {
	seen := map[string]bool{}
	if b.liveTasks != nil {
		for _, t := range b.liveTasks() {
			if t.Working && t.FromSession != "" {
				seen[t.FromSession] = true
			}
		}
	}
	if b.liveParents != nil {
		parents, err := b.liveParents(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("background set: replay")
		}
		for _, p := range parents {
			if p != "" {
				seen[p] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// replayAll re-reports every waiting session after a restart or a
// handover: nothing changes then, so without it a thread whose work
// outlived the old process would stay silent until the next change.
func (b *backgroundSets) replayAll(ctx context.Context) int {
	sessions := b.waitingSessions(ctx)
	for _, s := range sessions {
		b.report(s)
	}
	return len(sessions)
}

// backgroundReplayAfter is when the boot replay runs: once the channels
// had time to connect, and again later for one that connected slowly.
// Re-reporting the same set is harmless (the channels repaint it).
var backgroundReplayAfter = []time.Duration{10 * time.Second, 60 * time.Second}

// replayBackgroundSets runs replayAll at each backgroundReplayAfter mark.
func replayBackgroundSets(ctx context.Context, b *backgroundSets) {
	start := time.Now()
	for _, at := range backgroundReplayAfter {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(start.Add(at))):
		}
		if n := b.replayAll(ctx); n > 0 {
			log.Info().Int("sessions", n).Msg("background set: replayed")
		}
	}
}

// clipTitle shortens a task title for a channel ping.
func clipTitle(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// teamTaskMoved is the OnTeamTaskMoved hook: a just-sent working task gets
// its start ping (hidden ones too, the channel decides to drop it), and
// the session's set is reported on every move.
func teamTaskMoved(b *backgroundSets, hidden func(string) bool, start func(sessionID string, mate agentchannels.DetachedSurvivor, task string)) func(string, teamlink.TaskView, bool) {
	return func(sessionID string, v teamlink.TaskView, first bool) {
		if first && v.State == "working" && start != nil {
			mate := agentchannels.DetachedSurvivor{Handle: v.ToHandle, Teammate: true, Hidden: hidden != nil && hidden(sessionID)}
			start(sessionID, mate, clipTitle(v.Title, 80))
		}
		b.report(sessionID)
	}
}

// reportAgent re-reports every session agentID is waiting in, after its
// "Show teammates in channels" changed.
func (b *backgroundSets) reportAgent(agentID string) {
	if b.liveTasks == nil || agentID == "" {
		return
	}
	seen := map[string]bool{}
	for _, t := range b.liveTasks() {
		if t.FromAgentID == agentID && t.Working && !seen[t.FromSession] {
			seen[t.FromSession] = true
			b.report(t.FromSession)
		}
	}
}
