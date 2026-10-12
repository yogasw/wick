package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// failover.go is the decision half of failover: which instance a turn
// moves to when its provider fails, and which one a session should be on
// once things recover. Pure — the caller hands in the instances, the
// health check and the switch guard, so the whole choice is testable
// without a registry, a pool or a running agent.

// FailoverRequest is what the agent reader hands the pool when a turn
// fails with a failover trigger: who failed, why, and what the turn had
// produced so far.
type FailoverRequest struct {
	SessionID string
	AgentName string
	// Instance is the "type/name" key of the instance that failed.
	Instance string
	Kind     event.ErrKind
	// ResetAt is when the vendor said the limit lifts (zero = unknown).
	ResetAt time.Time
	// Text is the user message the turn was answering, as sent (sender
	// line included).
	Text string
	// Produced: the turn already emitted text or ran a tool.
	Produced bool
	// Snapshot is the turn's output so far, synthetic limit text excluded.
	Snapshot TurnSnapshot
	// ErrorMsg is the Error's own text: what consumers get when the
	// failover cannot go through after all.
	ErrorMsg string
	// MsgKey is the failover run this agent was spawned to carry on
	// (Options.FailoverMsgKey), set only for the turn that run re-sent;
	// "" = the turn is a message of its own.
	MsgKey string
}

// FailoverVerdict is the pool's answer. Rerun=false leaves the error to
// surface as it does without failover.
type FailoverVerdict struct {
	Rerun bool
	// Target is the "type/name" key the turn carries on with, ModelID
	// its model pin ("" = the target's default).
	Target  string
	ModelID string
	// Notice is the text the failing turn's error is replaced with.
	Notice string
}

// Target is one candidate PickTarget settled on.
type Target struct {
	// Instance is the canonical "type/name" tag.
	Instance string
	ModelID  string
	// Home: the target is the home instance itself.
	Home bool
}

// PickInput is everything PickTarget decides on.
type PickInput struct {
	// HomeProvider / HomeModelID are the session's home (FailoverState).
	// The chain followed is always the home's, never the current
	// provider's, so a target's own chain can't send the turn back.
	HomeProvider string
	HomeModelID  string
	// Current is the "type/name" key the session runs on now: in failover
	// mode the instance that just failed.
	Current string
	// Instances is the configured provider list (Load).
	Instances []Instance
	// Tried are the instance keys this run already failed on.
	Tried []string
	// Hops is how many automatic switches this run already made.
	Hops int
	// ReturnHome picks the session's rightful instance at a turn boundary
	// instead of a failover target: the highest candidate that is usable
	// now, the current one included. Tried, the hop cap and the shared
	// login check do not apply.
	ReturnHome bool
	// StopAtCurrent (ReturnHome mode) only considers candidates ABOVE
	// Current in the home's chain: a session never moves down it at a
	// turn boundary — a current instance gone bad is the failover's
	// business. Current missing from the chain (it was edited) leaves the
	// home as the only candidate.
	StopAtCurrent bool
	// HomeOnly limits the candidates to the home itself (ReturnHome mode
	// with the home's failover switched off).
	HomeOnly bool
	// Healthy reports whether an account is usable now; nil = HealthyNow.
	Healthy func(key string, now time.Time) (bool, Health)
	// Key names the account behind an instance; nil = HealthKey.
	Key func(Instance) string
	// Guard refuses a target the session may not use (CheckSwitch with the
	// session's ctx and id); nil = no guard.
	Guard func(tag string) error
	// Now is the health clock; zero = time.Now().
	Now time.Time
	// OnSkip, when set, hears every candidate passed over and why.
	OnSkip func(tag, reason string)
}

// Reasons OnSkip reports.
const (
	SkipCurrent      = "current"
	SkipTried        = "tried"
	SkipMissing      = "missing"
	SkipDisabled     = "disabled"
	SkipUnhealthy    = "unhealthy"
	SkipGuard        = "guard"
	SkipSameLogin    = "same login"
	SkipModelMissing = "model missing"
)

// FailoverHopCap is the most automatic switches one message may make
// along a chain of chainLen targets.
func FailoverHopCap(chainLen int) int {
	return min(chainLen, MaxFailoverTargets)
}

// PickTarget chooses where a session goes. Candidates are the home
// followed by the home's chain, in order.
//
// Failover mode: the first candidate that survives every check wins. The
// home is one of them only in a NEW run (the session was already on a
// stand-in when this message failed); within one run the home is in
// Tried — the run started by leaving it — so A→B→A cannot happen. Fill
// Tried/Hops from FailoverState.RunFor. Nothing is picked when the home's
// failover is off, the hop cap is spent, or every candidate is skipped.
//
// ReturnHome mode: the highest candidate usable now wins, home first; it
// may be Current itself, meaning "stay". Nothing is picked when the
// home's ReturnHome is off.
//
// Skipped: the instance that just failed (Current), an instance already
// tried, one missing or disabled, an unhealthy account, a target the
// guard refuses, one on the same login as an instance that failed in this
// run (Current or Tried), and a model pin the instance does not offer.
func PickTarget(in PickInput) (Target, bool) {
	healthy, key := in.Healthy, in.Key
	if healthy == nil {
		healthy = HealthyNow
	}
	if key == nil {
		key = HealthKey
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	skip := func(tag, reason string) {
		if in.OnSkip != nil {
			in.OnSkip(tag, reason)
		}
	}

	homeTyp, homeName := SplitInstanceTag(in.HomeProvider)
	home, ok := failoverFindIn(in.Instances, homeTyp, homeName)
	if !ok {
		skip(InstanceTag(homeTyp, homeName), SkipMissing)
		return Target{}, false
	}
	cfg := EffectiveFailover(home)
	if in.ReturnHome && !cfg.ReturnHome {
		return Target{}, false
	}
	if !in.ReturnHome && (!cfg.Enabled || in.Hops >= FailoverHopCap(len(cfg.Chain))) {
		return Target{}, false
	}

	curTyp, curName := SplitInstanceTag(in.Current)
	current := InstanceTag(curTyp, curName)
	// failedKeys: the logins this run already saw fail.
	failedKeys := map[string]bool{}
	if cur, ok := failoverFindIn(in.Instances, curTyp, curName); ok && in.Current != "" {
		failedKeys[key(cur)] = true
	}
	tried := map[string]bool{}
	for _, t := range in.Tried {
		typ, name := SplitInstanceTag(t)
		tried[InstanceTag(typ, name)] = true
		if ins, ok := failoverFindIn(in.Instances, typ, name); ok {
			failedKeys[key(ins)] = true
		}
	}

	candidates := append([]FailoverTarget{{Instance: InstanceTag(home.Type, home.Name), ModelID: in.HomeModelID}}, cfg.Chain...)
	if in.ReturnHome && in.StopAtCurrent {
		limit := 1
		for i, c := range candidates {
			typ, name := SplitInstanceTag(c.Instance)
			if in.Current != "" && InstanceTag(typ, name) == current {
				limit = i
				break
			}
		}
		candidates = candidates[:limit]
	}
	if in.ReturnHome && in.HomeOnly && len(candidates) > 1 {
		candidates = candidates[:1]
	}
	for i, c := range candidates {
		typ, name := SplitInstanceTag(c.Instance)
		tag := InstanceTag(typ, name)
		isCurrent := in.Current != "" && tag == current
		if !in.ReturnHome {
			switch {
			case isCurrent:
				skip(tag, SkipCurrent)
				continue
			case tried[tag]:
				skip(tag, SkipTried)
				continue
			}
		}
		ins, ok := failoverFindIn(in.Instances, typ, name)
		switch {
		case !ok:
			skip(tag, SkipMissing)
			continue
		case ins.Disabled:
			skip(tag, SkipDisabled)
			continue
		}
		k := key(ins)
		if !in.ReturnHome && failedKeys[k] {
			skip(tag, SkipSameLogin)
			continue
		}
		if ok, _ := healthy(k, now); !ok {
			skip(tag, SkipUnhealthy)
			continue
		}
		if !modelPinOffered(ins, c.ModelID) {
			skip(tag, SkipModelMissing)
			continue
		}
		// The session already runs on Current: staying needs no guard.
		if in.Guard != nil && !isCurrent {
			if err := in.Guard(tag); err != nil {
				skip(tag, SkipGuard)
				continue
			}
		}
		return Target{Instance: tag, ModelID: c.ModelID, Home: i == 0}, true
	}
	return Target{}, false
}

// GuardFor adapts CheckSwitch to PickInput.Guard for one session.
func GuardFor(ctx context.Context, sessionID string) func(tag string) error {
	return func(tag string) error { return CheckSwitch(ctx, sessionID, tag) }
}

// FailoverTrigger is the chain trigger an error kind fires ("" = none).
func FailoverTrigger(k event.ErrKind) string {
	switch k {
	case event.ErrLimit:
		return FailoverTriggerLimit
	case event.ErrAuth:
		return FailoverTriggerAuth
	case event.ErrUnavailable:
		return FailoverTriggerUnavailable
	}
	return ""
}

// FailoverReason says an error kind in words, for notices and the
// continue message.
func FailoverReason(k event.ErrKind, resetAt time.Time) string {
	var s string
	switch k {
	case event.ErrLimit:
		s = "usage limit reached"
	case event.ErrAuth:
		s = "login failed"
	case event.ErrUnavailable:
		s = "provider unavailable"
	default:
		s = "provider error"
	}
	if !resetAt.IsZero() {
		s += " (resets " + resetAt.UTC().Format("2006-01-02 15:04 UTC") + ")"
	}
	return s
}

// FailoverMsgKey identifies the user turn a failover run is for
// (FailoverState.MsgKey): the ORIGINAL message, never a continue message,
// so a second hop keeps the run's tried set, plus turnMark — something
// unique to that one send (the pool's send sequence, the user turn's
// timestamp), so a repeated "ok" is a run of its own. Both empty = ""
// (RunFor: always a new run).
func FailoverMsgKey(text, turnMark string) string {
	if text == "" && turnMark == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(turnMark + "\x00" + text))
	return hex.EncodeToString(sum[:8])
}
