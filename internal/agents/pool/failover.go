package pool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
)

// failover.go carries a turn that failed on its provider (usage limit,
// broken login, provider down) on to the next instance of the home's
// failover chain. The agent reader asks decideFailover synchronously,
// before the Error reaches anyone; a Rerun verdict turns that Error into
// a Warning and runFailover, in its own goroutine, stops the old process,
// switches the session (provider.ApplySwitch, failover mode, conditional
// on the agent still being on the instance that failed) and re-sends the
// turn: the original message when nothing came out yet, a continue
// message otherwise.
//
// While that runs the agent is "failing over" (foActive): new messages
// wait in the buffer and a manual switch does not kill anything. The old
// process is "retired" (foRetired) until its exit arrives, so that exit —
// on time or late — publishes no turn end and never touches the entry of
// the process that replaced it. When the failover cannot go through
// (switch refused, re-send failed) the original Error reaches the
// consumers after all; a person stopping the session meanwhile cancels
// the re-send (foCancel).

// failoverRun is the pool's memory of one session agent's failover run,
// beyond the persisted FailoverState: the run's message key and the
// output of every hop so far.
type failoverRun struct {
	mu     sync.Mutex
	msgKey string
	snap   provider.TurnSnapshot
	// extra is what a hop re-sent behind its continue message: messages
	// queued on the failed process and held meanwhile. A snapshot keeps
	// only the run's first message, so the next hop re-sends these again.
	extra []string
}

// resendMark is a failover re-send waiting for its spawn: the run it
// carries on, where it came from (to go back to when the target cannot
// start) and the wick text it put at the head of the buffer.
type resendMark struct {
	msgKey    string
	from      string
	fromModel string
	target    string
	cont      string
}

// failoverRunFor returns key's run memory, creating it.
func (p *Pool) failoverRunFor(key string) *failoverRun {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	if p.foRuns == nil {
		p.foRuns = map[string]*failoverRun{}
	}
	r := p.foRuns[key]
	if r == nil {
		r = &failoverRun{}
		p.foRuns[key] = r
	}
	return r
}

// FailingOver reports whether sessionID's agent is being moved to another
// provider right now.
func (p *Pool) FailingOver(sessionID, agentName string) bool {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	return p.foActive[sessionKey(sessionID, agentName)]
}

// setFailingOver marks key failing over with old as the retired process;
// false when it already was.
func (p *Pool) setFailingOver(key string, old *provider.Agent) bool {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	if p.foActive[key] {
		return false
	}
	if p.foActive == nil {
		p.foActive, p.foRetired = map[string]bool{}, map[string]*provider.Agent{}
	}
	p.foActive[key] = true
	if old != nil {
		p.foRetired[key] = old
	}
	return true
}

// retiredExit classifies an exit of key against the process a failover
// retired: retired = the exit is that process's (on time, or late after
// its replacement spawned), release = the entry still holds it and its
// slot must go. consume drops the record. No failover = false, false.
func (p *Pool) retiredExit(key string, consume bool) (retired, release bool) {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	old, ok := p.foRetired[key]
	if !ok {
		return p.foActive[key], p.foActive[key]
	}
	p.mu.Lock()
	entry, alive := p.active[key]
	p.mu.Unlock()
	switch {
	case !alive:
		retired = true
	case entry.agent == old:
		retired, release = true, true
	case !entry.agent.ExitReported():
		// The entry is the replacement, still running: this exit is not
		// its own.
		retired = true
	}
	if retired && consume {
		delete(p.foRetired, key)
	}
	return retired, release
}

// dropRetiredLocked forgets key's retired process once its exit has
// fired: a record left behind (its exit consumed nowhere) must not
// swallow an exit of the agent that replaced it. Caller holds foMu.
func (p *Pool) dropRetiredLocked(key string) {
	if old, ok := p.foRetired[key]; ok && old.ExitReported() {
		delete(p.foRetired, key)
	}
}

// cancelFailover marks key's failover, if one runs, as stopped by a
// person: the turn is not re-sent.
func (p *Pool) cancelFailover(key string) bool {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	if !p.foActive[key] {
		return false
	}
	if p.foCancel == nil {
		p.foCancel = map[string]bool{}
	}
	p.foCancel[key] = true
	return true
}

// cancelSessionFailovers is cancelFailover for every agent of sessionID.
func (p *Pool) cancelSessionFailovers(sessionID string) bool {
	p.foMu.Lock()
	var keys []string
	for k := range p.foActive {
		if strings.HasPrefix(k, sessionID+"::") {
			keys = append(keys, k)
		}
	}
	p.foMu.Unlock()
	any := false
	for _, k := range keys {
		any = p.cancelFailover(k) || any
	}
	return any
}

func (p *Pool) failoverCanceled(key string) bool {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	return p.foCancel[key]
}

// holdForFailover buffers a message sent while key fails over, so it
// reaches the next provider after the turn it interrupts. false = not
// failing over; the caller sends as usual. text is the agent-facing form.
func (p *Pool) holdForFailover(key, sessionID, text string) (bool, error) {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	if !p.foActive[key] {
		return false, nil
	}
	buf, err := p.bufferFor(sessionID)
	if err != nil {
		return true, err
	}
	return true, buf.Append(text)
}

// endFailingOver clears key's failover flags. deliver: messages held
// meanwhile go out now (they are a person's, sent after the error, as
// they would have been without failover); false leaves them buffered for
// the next send (the session was stopped).
func (p *Pool) endFailingOver(ctx context.Context, sessionID, agentName string, deliver bool) {
	key := sessionKey(sessionID, agentName)
	p.foMu.Lock()
	delete(p.foActive, key)
	delete(p.foCancel, key)
	p.dropRetiredLocked(key)
	held := 0
	if buf, err := p.bufferFor(sessionID); err == nil {
		held = buf.Len()
	}
	p.foMu.Unlock()
	if !deliver || held == 0 {
		return
	}
	if err := p.spawnOrQueue(ctx, sessionID, agentName, "failover"); err != nil {
		log.Warn().Err(err).Str("session", sessionID).Str("agent", agentName).Msg("pool.failover: delivering held messages failed")
	}
}

// decideFailover is provider.Options.OnFailover: whether the failed turn
// in req moves on, and where. It runs on the agent's reader, so it only
// decides — the stop, switch and re-send happen in runFailover.
//
// Eligible: the HOME instance has failover on and reacts to the error's
// kind, and a candidate of its chain survives PickTarget (health, the
// run's tried set and hop cap, and CheckSwitch — a Team agent that keeps
// its provider, or an owner without access to the target, refuses it).
// The instance that failed is marked unhealthy either way.
func (p *Pool) decideFailover(req provider.FailoverRequest) provider.FailoverVerdict {
	var no provider.FailoverVerdict
	trigger := provider.FailoverTrigger(req.Kind)
	if trigger == "" || req.SessionID == "" || req.Instance == "" {
		return no
	}
	key := sessionKey(req.SessionID, req.AgentName)
	if p.FailingOver(req.SessionID, req.AgentName) {
		return no
	}
	l := log.With().Str("component", "pool").Str("session", req.SessionID).Str("agent", req.AgentName).Logger()

	sess, cur, ok := p.sessionAgent(req.SessionID, req.AgentName)
	if !ok || skipsFailover(sess.Meta) {
		return no
	}
	instances, err := provider.Load()
	if err != nil {
		return no
	}
	failedKey := normProviderKey(req.Instance)
	home, homeModel := failedKey, cur.ModelID
	if cur.Failover != nil && cur.Failover.HomeProvider != "" {
		home, homeModel = normProviderKey(cur.Failover.HomeProvider), cur.Failover.HomeModelID
	}
	homeIns, ok := instanceByTag(instances, home)
	if !ok {
		return no
	}
	cfg := provider.EffectiveFailover(homeIns)
	if !cfg.Enabled || !cfg.HasTrigger(trigger) {
		return no
	}
	now := time.Now()
	reason := provider.FailoverReason(req.Kind, req.ResetAt)
	if failed, ok := instanceByTag(instances, failedKey); ok {
		provider.MarkInstanceUnhealthy(failed, provider.Health{Kind: req.Kind, Until: req.ResetAt, Reason: reason})
	}

	run := p.failoverRunFor(key)
	run.mu.Lock()
	defer run.mu.Unlock()
	// The spawn a run re-sent its turn to reports that run's key on its
	// first turn: the tried set carries on and the snapshot grows.
	// Anything else is a message of its own, hence a new run.
	if req.MsgKey != "" && req.MsgKey == run.msgKey {
		run.snap = run.snap.Combine(req.Snapshot)
	} else {
		run.msgKey = provider.FailoverMsgKey(req.Text, now.Format(time.RFC3339Nano))
		run.snap, run.extra = req.Snapshot, nil
	}
	st := cur.Failover.RunFor(run.msgKey, now)
	t, ok := provider.PickTarget(provider.PickInput{
		HomeProvider: home,
		HomeModelID:  homeModel,
		Current:      failedKey,
		Instances:    instances,
		Tried:        st.Tried,
		Hops:         st.Hops,
		Guard:        provider.GuardFor(context.Background(), req.SessionID),
		Now:          now,
		OnSkip: func(tag, why string) {
			l.Debug().Str("candidate", tag).Str("reason", why).Msg("pool.failover: candidate skipped")
		},
	})
	if !ok {
		l.Info().Str("from", failedKey).Str("reason", reason).Msg("pool.failover: no usable target, the error stands")
		return no
	}

	// Taken here, on the reader, not in runFailover: the old process can
	// exit on its own before that goroutine looks, taking its queue along.
	p.mu.Lock()
	entry := p.active[key]
	p.mu.Unlock()
	job := failoverJob{req: req, from: failedKey, fromModel: cur.ModelID, target: t, reason: reason, msgKey: run.msgKey, snap: run.snap, extra: run.extra, ctx: context.Background()}
	if entry != nil {
		job.agent, job.store = entry.agent, entry.store
		if entry.ctx != nil {
			job.ctx = entry.ctx
		}
	}
	p.failoverStage("deciding")
	if !p.setFailingOver(key, job.agent) {
		return no
	}
	if job.agent != nil {
		job.pending = job.agent.TakePending()
	}
	l.Info().Str("from", failedKey).Str("to", t.Instance).Str("reason", reason).Msg("pool.failover: moving the turn")
	go p.runFailover(job)
	return provider.FailoverVerdict{
		Rerun:   true,
		Target:  t.Instance,
		ModelID: t.ModelID,
		Notice:  provider.ChannelLabel(failedKey, "") + " stopped: " + reason + ". Continuing on " + provider.ChannelLabel(t.Instance, t.ModelID) + ".",
	}
}

// failoverJob is one decided failover, handed to runFailover.
type failoverJob struct {
	req       provider.FailoverRequest
	from      string
	fromModel string
	target    provider.Target
	reason    string
	msgKey    string
	snap      provider.TurnSnapshot
	extra     []string
	ctx       context.Context
	agent     *provider.Agent
	store     *store.Store
	pending   []string
}

// runFailover carries a decided failover out: stop the old process
// SYNCHRONOUSLY (a re-send must never reach the account that just
// failed, and the reader must be done writing agents.json before the
// switch rewrites it), switch — only if the agent is still on the
// instance that failed; a manual switch that landed meanwhile wins and
// gets the turn — then re-send.
func (p *Pool) runFailover(j failoverJob) {
	sid, name := j.req.SessionID, j.req.AgentName
	key := sessionKey(sid, name)
	l := log.With().Str("component", "pool").Str("session", sid).Str("agent", name).Logger()

	if j.agent != nil {
		if j.store != nil {
			j.store.SetInterruptCause("wick", "the provider stopped mid-turn ("+j.reason+"); the turn continues on "+j.target.Instance)
		}
		j.agent.NoteStopCause("failover: " + j.reason)
		if err := j.agent.Stop(); err != nil {
			l.Warn().Err(err).Msg("pool.failover: stopping the failed process")
		}
		// Stop gives up waiting after a grace period; the exit hook may
		// not have released the slot yet. Release it here — the late
		// exit is recognised as retired and ignored, so the queue grant
		// its exit hook would make happens here too.
		p.mu.Lock()
		e, ok := p.active[key]
		stale := ok && e.agent == j.agent
		p.mu.Unlock()
		if stale {
			p.releaseSlot(key)
			p.tryGrantQueue()
		}
		// Written to the process after decideFailover took its queue:
		// it never read them.
		j.pending = append(j.pending, j.agent.TakePending()...)
	}
	p.failoverStage("stopped")
	if p.failoverCanceled(key) {
		p.requeueHeld(sid, j.pending)
		p.endFailingOver(j.ctx, sid, name, false)
		p.abortFailover(sid, name)
		return
	}

	snap, kind, resetAt := j.snap, j.req.Kind, j.req.ResetAt
	res, err := provider.ApplySwitch(p.cfg.Layout, sid, name, j.target.Instance, provider.SwitchOptions{
		Source:         "failover",
		ModelID:        j.target.ModelID,
		Failover:       true,
		FailoverReason: j.reason,
		MsgKey:         j.msgKey,
		ExpectFrom:     j.from,
		ContinueFor: func(r provider.SwitchResult) string {
			return provider.ContinueMessage(provider.ContinueInputFrom(r, kind, resetAt, snap))
		},
		Notify: func(tag string, steps []string) {
			if p.cfg.OnFailoverNotify != nil {
				p.cfg.OnFailoverNotify(sid, name, provider.FailoverChipText(tag, j.target.ModelID), steps)
			}
		},
		Reply: func(text string) {
			if p.cfg.OnFailoverReply != nil {
				p.cfg.OnFailoverReply(sid, name, text)
			}
		},
	})
	cont := ""
	switch {
	case errors.Is(err, provider.ErrSwitchedMeanwhile):
		// A person switched after the failure: their pick gets the turn.
		a, _ := p.agentEntry(sid, name)
		cont = provider.ContinueMessage(provider.ContinueInput{
			From: j.from, To: normProviderKey(a.Provider), Reason: kind, ResetAt: resetAt,
			UserMsg: snap.UserMsg, Produced: snap.Produced, PartialText: snap.Text, Tools: snap.Tools,
		})
		l.Info().Str("to", a.Provider).Msg("pool.failover: switched by hand meanwhile, re-sending there")
	case err != nil:
		l.Error().Err(err).Str("to", j.target.Instance).Msg("pool.failover: switch failed, the error stands")
		p.abortFailover(sid, name, failedTurnError(j.req))
		// Messages queued on the failed process go out with those held
		// meanwhile, as they would have after the error.
		p.requeueHeld(sid, j.pending)
		p.endFailingOver(j.ctx, sid, name, true)
		return
	default:
		cont = res.Continue
	}
	p.failoverStage("switched")
	if p.failoverCanceled(key) {
		p.requeueHeld(sid, j.pending)
		p.endFailingOver(j.ctx, sid, name, false)
		p.abortFailover(sid, name)
		return
	}
	// What follows the continue message: this hop's queue, after what an
	// earlier hop of the run re-sent (its snapshot does not carry that).
	extra := append(append([]string(nil), j.extra...), j.pending...)
	p.foMu.Lock()
	if p.foResend == nil {
		p.foResend = map[string]resendMark{}
	}
	p.foResend[key] = resendMark{msgKey: j.msgKey, from: j.from, fromModel: j.fromModel, target: normProviderKey(j.target.Instance), cont: cont}
	p.foMu.Unlock()
	if buf, err := p.bufferFor(sid); err == nil {
		// Held messages go out behind it too: a further hop needs them.
		extra = append(extra, buf.Lines()...)
	}
	p.foMu.Lock()
	run := p.foRuns[key]
	p.foMu.Unlock()
	if run != nil {
		run.mu.Lock()
		if run.msgKey == j.msgKey {
			run.extra = extra
		}
		run.mu.Unlock()
	}
	p.failoverStage("resending")
	err = p.Resend(j.ctx, sid, name, append(append([]string{cont}, j.extra...), j.pending...)...)
	switch {
	case errors.Is(err, errFailoverCanceled):
		// Stopped by a person right before the re-send: end the turn.
		p.takeResend(key)
		p.requeueHeld(sid, j.pending)
		p.abortFailover(sid, name)
	case err != nil:
		// A spawn that failed took the mark (and cleaned up after itself);
		// an earlier failure left it.
		p.takeResend(key)
		l.Error().Err(err).Msg("pool.failover: re-send failed, the error stands")
		p.endFailingOver(j.ctx, sid, name, false)
		p.abortFailover(sid, name, failedTurnError(j.req))
	}
}

// requeueHeld puts messages taken off a failed process back at the head
// of the session's buffer when the failover does not re-send them: they
// are a person's, already in conversation.jsonl, and go out with the next
// spawn like anything else held.
func (p *Pool) requeueHeld(sessionID string, texts []string) {
	if len(texts) == 0 {
		return
	}
	buf, err := p.bufferFor(sessionID)
	if err == nil {
		err = buf.Prepend(texts...)
	}
	if err != nil {
		log.Warn().Err(err).Str("session", sessionID).Int("messages", len(texts)).Msg("pool.failover: requeueing held messages failed")
	}
}

// resendSpawnFailed cleans up after a failover re-send whose spawn on the
// target failed: the continue message leaves the buffer (the turn's error
// stands; replaying it with the next message would answer a stale turn).
// When the provider itself failed to build or start (providerFault), the
// target is also marked unavailable and the agent goes back to the
// instance it failed over from — left on a target that cannot start,
// every later message would fail the same way. Any other cause (the pool
// closing, the session or its workspace failing to load) says nothing
// about the target: the run ends and the agent stays where it is. Messages
// of a person stay buffered for the next spawn.
func (p *Pool) resendSpawnFailed(sessionID, agentName string, m resendMark, cause error, providerFault bool) {
	l := log.With().Str("component", "pool").Str("session", sessionID).Str("agent", agentName).Logger()
	if buf, err := p.bufferFor(sessionID); err == nil && m.cont != "" {
		if _, err := buf.DropHead(m.cont); err != nil {
			l.Warn().Err(err).Msg("pool.failover: dropping the continue message failed")
		}
	}
	if !providerFault {
		key := sessionKey(sessionID, agentName)
		p.foMu.Lock()
		delete(p.foRuns, key)
		p.foMu.Unlock()
		_ = session.EndFailoverRun(p.cfg.Layout, sessionID, agentName)
		l.Info().Err(cause).Str("target", m.target).Msg("pool.failover: re-send not started, target left as is")
		return
	}
	reason := "failed to start"
	if cause != nil {
		reason += ": " + cause.Error()
	}
	if ins, ok := instanceByTag(nil, m.target); ok {
		provider.MarkInstanceUnhealthy(ins, provider.Health{Kind: event.ErrUnavailable, Reason: reason})
	}
	if m.from == "" {
		return
	}
	_, err := provider.ApplySwitch(p.cfg.Layout, sessionID, agentName, m.from, provider.SwitchOptions{
		Source:     "failover",
		ModelID:    m.fromModel,
		ReturnHome: true,
		ExpectFrom: m.target,
	})
	if err != nil {
		l.Warn().Err(err).Str("to", m.from).Msg("pool.failover: going back after a failed start")
		return
	}
	l.Info().Str("from", m.target).Str("to", m.from).Msg("pool.failover: target failed to start, back where the turn failed")
}

// errFailoverCanceled is Resend's refusal when a person stopped the
// session after runFailover's last check: nothing is re-sent.
var errFailoverCanceled = errors.New("failover canceled")

// failoverStage is a test seam between runFailover's steps.
func (p *Pool) failoverStage(stage string) {
	if p.foStage != nil {
		p.foStage(stage)
	}
}

// failedTurnError is the Error a failover swallowed, as the CLI said it.
func failedTurnError(req provider.FailoverRequest) event.AgentEvent {
	msg := req.ErrorMsg
	if msg == "" {
		msg = provider.FailoverReason(req.Kind, req.ResetAt)
	}
	return event.AgentEvent{Type: event.Error, ErrorMsg: msg, ErrKind: req.Kind, ResetAt: req.ResetAt}
}

// abortFailover ends a turn a failover had taken over but could not carry
// on: evs (the original Error, when there is one) are recorded and
// published, then the Done the old process's exit did not publish.
func (p *Pool) abortFailover(sessionID, agentName string, evs ...event.AgentEvent) {
	// The run is over: a later message starts its own, with nothing of
	// this one's snapshot, tried set or re-send key.
	key := sessionKey(sessionID, agentName)
	p.foMu.Lock()
	delete(p.foRuns, key)
	delete(p.foResend, key)
	p.foMu.Unlock()
	_ = session.EndFailoverRun(p.cfg.Layout, sessionID, agentName)
	// The old process's exit was retired and set no status: nothing runs.
	_ = p.markStatus(sessionID, session.StatusIdle)
	if len(evs) > 0 {
		sto := store.New(store.Options{Layout: p.cfg.Layout, SessionID: sessionID, AgentName: agentName})
		for _, ev := range evs {
			_, _ = sto.Apply(ev)
		}
	}
	if p.cfg.OnFailoverAbort != nil {
		p.cfg.OnFailoverAbort(sessionID, agentName, append(evs, event.AgentEvent{Type: event.Done}))
	}
}

// Resend delivers texts to sessionID's agent as the head of its next
// turn without recording them: they are a turn already in
// conversation.jsonl (or wick's own continue message), so no user bubble
// is written and no user-message hook fires. Empty texts are skipped.
//
// It also ends the agent's failover, before spawning: what is held is in
// the buffer behind texts by then, and the spawn's own exit or failure
// must reach the consumers like any other.
func (p *Pool) Resend(ctx context.Context, sessionID, agentName string, texts ...string) error {
	var keep []string
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			keep = append(keep, t)
		}
	}
	key := sessionKey(sessionID, agentName)
	p.foMu.Lock()
	// The cancel check and the end of the failover share one critical
	// section with Kill's cancelFailover: a stop either lands before (no
	// re-send) or after (it stops the spawned agent like any other).
	if p.foCancel[key] {
		delete(p.foActive, key)
		delete(p.foCancel, key)
		p.dropRetiredLocked(key)
		p.foMu.Unlock()
		return errFailoverCanceled
	}
	buf, err := p.bufferFor(sessionID)
	if err == nil && len(keep) > 0 {
		err = buf.Prepend(keep...)
	}
	delete(p.foActive, key)
	delete(p.foCancel, key)
	p.foMu.Unlock()
	if err != nil {
		return err
	}
	if buf.Len() == 0 {
		return nil
	}
	return p.spawnOrQueue(ctx, sessionID, agentName, "failover")
}

// takeResend hands a spawn the failover re-send it carries on (once).
func (p *Pool) takeResend(key string) resendMark {
	p.foMu.Lock()
	defer p.foMu.Unlock()
	m := p.foResend[key]
	delete(p.foResend, key)
	return m
}

// turnOK is provider.Options.OnTurnOK for an agent on instance tag: a
// turn that ended well proves the account works (a failure recorded
// before the turn's message is cleared) and ends the failover run. Both
// are skipped without touching disk when there is nothing to clear.
func (p *Pool) turnOK(sessionID, agentName, tag string, sentAt time.Time) {
	if ins, ok := instanceByTag(nil, tag); ok {
		hk := provider.HealthKey(ins)
		if _, bad := provider.HealthOf(hk); bad {
			provider.MarkHealthyIf(hk, sentAt)
		}
	}
	key := sessionKey(sessionID, agentName)
	p.foMu.Lock()
	_, inRun := p.foRuns[key]
	delete(p.foRuns, key)
	p.dropRetiredLocked(key)
	p.foMu.Unlock()
	if inRun {
		_ = session.EndFailoverRun(p.cfg.Layout, sessionID, agentName)
	}
}

// maybeReturnHome moves sessionID's agent back up its home's failover
// chain at a turn boundary: right before a new user message goes out,
// while nothing runs. A session on a stand-in whose home (or a target
// above the current one in the home's chain — never below it) is usable
// again has its idle process stopped and switches there (ApplySwitch,
// ReturnHome, conditional on the agent still being on the stand-in); the
// message then spawns on the instance it moved to. true = it switched.
// Either way the caller re-reads the live entry before sending.
//
// Usable is HealthyNow, which reads the usage file first: a limit
// someone reset by hand counts as cleared before its Until. A limited
// account whose file is stale gets a paced refresh (RequestRefresh,
// non-blocking) — this send stays where it is and a later one sees the
// answer. A home whose failover was switched off is still returned to
// (when ReturnHome is on), but only the home, never another target.
//
// Never cuts a turn short: a busy agent, a spawn in flight or a failover
// in progress skip the check, and the stop itself is Agent.StopIfIdle —
// a message that reached the process after the first look keeps it, and
// the session stays on the stand-in. Cheap when there is nothing to
// return from: one agents.json read, no provider registry load. When it
// does switch, the switch and the stop run on the sender's goroutine:
// that send waits for them.
func (p *Pool) maybeReturnHome(sessionID, agentName string) bool {
	key := sessionKey(sessionID, agentName)
	if p.FailingOver(sessionID, agentName) {
		return false
	}
	p.mu.Lock()
	entry, alive := p.active[key]
	_, spawning := p.spawningKeys[key]
	p.mu.Unlock()
	if spawning || (alive && !entryIdle(entry)) {
		return false
	}
	sess, a, ok := p.sessionAgent(sessionID, agentName)
	if !ok || skipsFailover(sess.Meta) || a.Failover == nil || a.Failover.HomeProvider == "" {
		return false
	}
	cur, home := normProviderKey(a.Provider), normProviderKey(a.Failover.HomeProvider)
	if cur == "" || cur == home {
		return false
	}
	instances, err := provider.Load()
	if err != nil {
		return false
	}
	homeIns, ok := instanceByTag(instances, home)
	if !ok {
		return false
	}
	l := log.With().Str("component", "pool").Str("session", sessionID).Str("agent", agentName).Logger()
	t, ok := provider.PickTarget(provider.PickInput{
		HomeProvider:  home,
		HomeModelID:   a.Failover.HomeModelID,
		Current:       cur,
		Instances:     instances,
		ReturnHome:    true,
		StopAtCurrent: true,
		// Failover off for the home: nothing but the home to go back to.
		HomeOnly: !provider.EffectiveFailover(homeIns).Enabled,
		Healthy:  healthyOrRefresh,
		Guard:    provider.GuardFor(context.Background(), sessionID),
		OnSkip: func(tag, why string) {
			l.Debug().Str("candidate", tag).Str("reason", why).Msg("pool.failover: return candidate skipped")
		},
	})
	if !ok || t.Instance == cur {
		return false
	}
	p.failoverStage("return-picked")
	var old *provider.Agent
	if alive {
		old = entry.agent
	}
	// Failing over for the length of the switch: a message sent meanwhile
	// is held in the buffer instead of reaching the process about to stop,
	// and that process's exit is recognised as retired. Held messages end
	// up in the same prompt as the caller's, AHEAD of it (they were
	// buffered first) — in conversation.jsonl too, so the transcript and
	// the prompt agree; only the arrival order is lost.
	if !p.setFailingOver(key, old) {
		return false
	}
	// The first look was a while ago (agents.json, the registry, usage
	// files, the switch guard): the agent must still be the one seen,
	// idle, and nothing may be spawning it.
	p.mu.Lock()
	e, live := p.active[key]
	_, spawning = p.spawningKeys[key]
	same := !spawning && ((old == nil && !live) || (old != nil && live && e.agent == old && entryIdle(e)))
	p.mu.Unlock()
	if !same || (old != nil && !old.StopIfIdle()) {
		l.Debug().Bool("same", same).Msg("pool.failover: agent moved on before the return, staying")
		p.abortReturn(key, sessionID, agentName, old)
		return false
	}
	if old != nil {
		p.releaseSlotIf(key, old)
	}
	_, err = provider.ApplySwitch(p.cfg.Layout, sessionID, agentName, t.Instance, provider.SwitchOptions{
		Source:     "failover",
		ModelID:    t.ModelID,
		ReturnHome: true,
		ExpectFrom: cur,
		Notify: func(tag string, steps []string) {
			if p.cfg.OnFailoverNotify != nil {
				p.cfg.OnFailoverNotify(sessionID, agentName, provider.ReturnChipText(tag, t.ModelID), steps)
			}
		},
		Reply: func(text string) {
			if p.cfg.OnFailoverReply != nil {
				p.cfg.OnFailoverReply(sessionID, agentName, text)
			}
		},
	})
	switch {
	case errors.Is(err, provider.ErrSwitchedMeanwhile):
		// A person (or a concurrent return) switched meanwhile: that
		// stands. Their switch left the process alone while this one held
		// the agent; it is stopped already, the next spawn uses theirs.
		l.Info().Str("to", t.Instance).Msg("pool.failover: switched meanwhile, not returning")
	case err != nil:
		// The stand-in process is gone but agents.json still names it:
		// the message respawns it.
		l.Warn().Err(err).Str("to", t.Instance).Msg("pool.failover: return switch failed, staying")
	default:
		l.Info().Str("from", cur).Str("to", t.Instance).Bool("home", t.Home).Msg("pool.failover: back up the chain")
		p.foMu.Lock()
		delete(p.foRuns, key)
		delete(p.foResend, key)
		p.foMu.Unlock()
	}
	// Held messages stay buffered: the caller's own spawn drains them.
	p.endFailingOver(context.Background(), sessionID, agentName, false)
	return err == nil
}

// abortReturn undoes maybeReturnHome's hold when the agent moved on
// before the stop: the process was not retired, and messages held
// meanwhile go to it (or to the spawn in flight) as they would have.
func (p *Pool) abortReturn(key, sessionID, agentName string, old *provider.Agent) {
	p.foMu.Lock()
	if old != nil && p.foRetired[key] == old {
		delete(p.foRetired, key)
	}
	p.foMu.Unlock()
	p.mu.Lock()
	e, live := p.active[key]
	p.mu.Unlock()
	if !live {
		// Not running (or spawning): endFailingOver's spawnOrQueue
		// delivers what was held, joining a spawn in flight.
		p.endFailingOver(context.Background(), sessionID, agentName, true)
		return
	}
	p.endFailingOver(context.Background(), sessionID, agentName, false)
	buf, err := p.bufferFor(sessionID)
	if err != nil || buf.Len() == 0 {
		return
	}
	if text, err := buf.Drain(); err == nil && text != "" {
		if err := e.agent.Send(text); err != nil {
			_ = buf.Prepend(text)
		}
	}
}

// entryIdle: a live agent between turns, nothing queued on it.
func entryIdle(e *runEntry) bool {
	if e == nil || e.state == nil || e.state.Lifecycle() != state.LifecycleIdle {
		return false
	}
	return e.agent == nil || e.agent.QueuedCount() == 0
}

// healthyOrRefresh is HealthyNow for a return-home pick, asking for a
// fresh usage reading of a limited account it turns down (RequestRefresh
// itself skips a fresh file).
func healthyOrRefresh(key string, now time.Time) (bool, provider.Health) {
	ok, h := provider.HealthyNow(key, now)
	if !ok && h.Kind == event.ErrLimit {
		provider.RequestRefresh(key)
	}
	return ok, h
}

// agentEntry reads one agent's entry from the session's agents.json.
func (p *Pool) agentEntry(sessionID, agentName string) (session.AgentEntry, bool) {
	_, a, ok := p.sessionAgent(sessionID, agentName)
	return a, ok
}

// sessionAgent is agentEntry with the session it was read from.
func (p *Pool) sessionAgent(sessionID, agentName string) (session.Session, session.AgentEntry, bool) {
	sess, err := session.Load(p.cfg.Layout, sessionID)
	if err != nil {
		return session.Session{}, session.AgentEntry{}, false
	}
	for _, a := range sess.Agents {
		if a.Name == agentName {
			return sess, a, true
		}
	}
	return sess, session.AgentEntry{}, false
}

// failoverHook is the spawn's OnFailover: none for a session failover
// leaves alone, so its turn errors reach the run as they are.
func failoverHook(m session.Meta, decide func(provider.FailoverRequest) provider.FailoverVerdict) func(provider.FailoverRequest) provider.FailoverVerdict {
	if skipsFailover(m) {
		return nil
	}
	return decide
}

// skipsFailover: sessions failover leaves alone. A workflow run picks its
// provider per node and reports a failed turn to the run; it never moves.
func skipsFailover(m session.Meta) bool {
	return m.Origin == session.OriginWorkflow
}

// normProviderKey is the "type/name" form of a provider key; a bare type
// ("claude", what older agents.json entries hold) is its default
// instance.
func normProviderKey(key string) string {
	if key == "" || strings.Contains(key, "/") {
		return key
	}
	return key + "/" + key
}

// instanceByTag finds the enabled instance a "type/name" (or bare type)
// tag names; instances nil = the provider registry (Find).
func instanceByTag(instances []provider.Instance, tag string) (provider.Instance, bool) {
	tag = normProviderKey(tag)
	t, name := tag, tag
	if i := strings.IndexByte(tag, '/'); i >= 0 {
		t, name = tag[:i], tag[i+1:]
	}
	if instances == nil {
		ins, err := provider.Find(provider.Type(t), name)
		return ins, err == nil
	}
	for _, ins := range instances {
		if string(ins.Type) == t && ins.Name == name && !ins.Disabled {
			return ins, true
		}
	}
	return provider.Instance{}, false
}
