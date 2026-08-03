package delegation

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/entity"
)

// Governor limits are SYSTEM-WIDE ceilings, deliberately separate from
// per-profile defaults. A profile expresses the identity of a role; the
// governor expresses what the system will tolerate regardless of role.
// Profile values are clamped to these — a profile can never raise them.
//
// Defaults are conservative: a runaway delegation tree burns real money,
// and the failure mode of a too-low cap (a partial result plus a clear
// stopped_* status) is far cheaper than the failure mode of a too-high
// one.
const (
	DefaultMaxDepth   = 3
	DefaultRootBudget = 40
	// DefaultMaxParallel is 1: a conversation runs ONE sub-agent at a
	// time and queues the rest. Several agents streaming into one room at
	// once is unreadable and burns the tree's budget in parallel, while
	// the cost of serial is waiting. The knob stays for operators who
	// have the headroom and want throughput.
	DefaultMaxParallel = 1
	DefaultMaxTurns    = 12
	// MaxTurnsCeiling caps what any single sub-agent may request, even
	// when both the profile and the caller ask for more.
	MaxTurnsCeiling = 50
	// DefaultMaxTokensPerDelegation bounds one sub-agent's spend. Turn
	// counting alone does not bound cost: one turn that reads a large
	// file can cost more than ten small ones.
	DefaultMaxTokensPerDelegation = 200_000
	// DefaultRootTokenBudget bounds spend across a whole delegation tree.
	DefaultRootTokenBudget = 1_000_000
	// DefaultMaxHops bounds consecutive agent-to-agent messages between
	// human turns. Ten is enough for a real exchange (ask, clarify,
	// answer, confirm) and short enough that a loop costs a few turns
	// rather than a whole budget. Turn budget alone is a poor brake here:
	// two agents can trade short messages cheaply for a long time.
	DefaultMaxHops = 10
	// DefaultMaxIterations bounds checker rounds per investigation.
	DefaultMaxIterations = 5
	// DefaultMaxRuntimeMinutes bounds an investigation by wall clock, for
	// the case turn counting cannot catch: an agent that is slow rather
	// than chatty.
	DefaultMaxRuntimeMinutes = 20
	// DefaultNoNewEvidenceRounds is 2, not 1. An investigation that comes
	// up empty once and lands the next round is the normal case, and
	// stopping at the first empty round throws the run away.
	DefaultNoNewEvidenceRounds = 2
)

// Limits is the resolved governor configuration.
type Limits struct {
	MaxDepth    int
	RootBudget  int
	MaxParallel int
	MaxTurnsCap int
	// MaxTokensPerDelegation caps spend for one sub-agent. 0 = no
	// per-delegation ceiling (the tree budget still applies).
	MaxTokensPerDelegation int
	// RootTokenBudget caps total spend across one delegation tree.
	// 0 = no token ceiling; turn limits remain the only brake.
	RootTokenBudget int
	// MaxHops bounds consecutive agent-to-agent messages between human
	// turns. Reset when a person speaks, never by an agent.
	MaxHops int
	// MaxIterations caps checker rounds for one investigation.
	MaxIterations int
	// MaxRuntimeMinutes caps an investigation by wall clock from the
	// moment its incident was created.
	MaxRuntimeMinutes int
	// MinConfidence is the enum floor for producing a customer-facing
	// draft: unknown | low | medium | high. An enum rather than a number
	// because a decimal from a language model is false precision.
	MinConfidence string
	// NoNewEvidenceRounds is how many consecutive rounds may add nothing
	// before the investigation stops.
	NoNewEvidenceRounds int
	// MentionRouter enables acting on @name at the start of a line.
	//
	// Deliberately NOT normalized: false is a real operator choice, so
	// there is nothing here to "repair" the way a zero turn cap is
	// repaired. Callers build Limits through DefaultLimits or the config
	// provider, both of which set it explicitly.
	MentionRouter bool
	// Disabled is the global kill-switch. When true, every delegation is
	// refused before anything spawns — the emergency stop and the
	// staged-rollout lever.
	Disabled bool
}

// DefaultLimits returns the built-in ceilings.
func DefaultLimits() Limits {
	return Limits{
		MaxDepth:               DefaultMaxDepth,
		RootBudget:             DefaultRootBudget,
		MaxParallel:            DefaultMaxParallel,
		MaxTurnsCap:            MaxTurnsCeiling,
		MaxTokensPerDelegation: DefaultMaxTokensPerDelegation,
		RootTokenBudget:        DefaultRootTokenBudget,
		MaxHops:                DefaultMaxHops,
		MentionRouter:          true,
		MaxIterations:          DefaultMaxIterations,
		MaxRuntimeMinutes:      DefaultMaxRuntimeMinutes,
		MinConfidence:          ConfidenceMedium,
		NoNewEvidenceRounds:    DefaultNoNewEvidenceRounds,
	}
}

// normalize replaces non-positive fields with their defaults so a
// partially-filled config row can never disable a limit by accident.
// A zero max_depth must mean "use the default", not "unlimited".
func (l Limits) normalize() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = DefaultMaxDepth
	}
	if l.RootBudget <= 0 {
		l.RootBudget = DefaultRootBudget
	}
	if l.MaxParallel <= 0 {
		l.MaxParallel = DefaultMaxParallel
	}
	if l.MaxTurnsCap <= 0 {
		l.MaxTurnsCap = MaxTurnsCeiling
	}
	if l.MaxIterations <= 0 {
		l.MaxIterations = DefaultMaxIterations
	}
	if l.MaxRuntimeMinutes <= 0 {
		l.MaxRuntimeMinutes = DefaultMaxRuntimeMinutes
	}
	if l.NoNewEvidenceRounds <= 0 {
		l.NoNewEvidenceRounds = DefaultNoNewEvidenceRounds
	}
	switch l.MinConfidence {
	case ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
	default:
		// Including "unknown": a gate that accepts an unvouched-for answer
		// is not a gate.
		l.MinConfidence = ConfidenceMedium
	}
	if l.MaxHops <= 0 {
		l.MaxHops = DefaultMaxHops
	}
	// Token ceilings are allowed to be 0 = "no token cap", unlike the
	// others: a provider that reports no usage cannot be capped on spend,
	// and pretending otherwise would stop legitimate work on those
	// providers. Turn limits stay the universal brake.
	if l.MaxTokensPerDelegation < 0 {
		l.MaxTokensPerDelegation = 0
	}
	if l.RootTokenBudget < 0 {
		l.RootTokenBudget = 0
	}
	return l
}

// RefusalReason explains why the governor rejected a delegation. It maps
// to the status the caller records and, for the leader, to a tool result
// that says plainly what happened.
type RefusalReason string

const (
	RefusedDisabled    RefusalReason = "disabled"
	RefusedDepth       RefusalReason = "max_depth"
	RefusedCycle       RefusalReason = "cycle"
	RefusedBudget      RefusalReason = "budget"
	RefusedTokenBudget RefusalReason = "token_budget"
	RefusedProfileGone RefusalReason = "profile_disabled"
	// RefusedHops means agents have been messaging each other for too
	// many consecutive turns with no human in the loop.
	RefusedHops RefusalReason = "max_hops"
)

// Refusal is a governor rejection carrying a human-readable message.
type Refusal struct {
	Reason  RefusalReason
	Message string
}

func (r *Refusal) Error() string { return r.Message }

// Status maps a refusal onto the delegation status it should be recorded
// as. Budget exhaustion is its own terminal status so an operator can
// tell "this tree hit its ceiling" from "this agent errored".
func (r *Refusal) Status() string {
	if r.Reason == RefusedBudget || r.Reason == RefusedTokenBudget {
		return entity.DelegationStoppedBudget
	}
	return entity.DelegationFailed
}

// EffectiveMaxTurns resolves the per-sub-agent turn cap:
// caller request, else profile default, else global default — then
// clamped to the ceiling. Clamping (rather than rejecting) keeps a
// too-ambitious request working at a safe value instead of failing.
func EffectiveMaxTurns(requested int, profile *entity.AgentProfile, lim Limits) int {
	lim = lim.normalize()
	n := requested
	if n <= 0 && profile != nil {
		n = profile.DefaultMaxTurns
	}
	if n <= 0 {
		n = DefaultMaxTurns
	}
	if n > lim.MaxTurnsCap {
		n = lim.MaxTurnsCap
	}
	return n
}

// EffectiveMaxTokens resolves the per-delegation token cap: caller
// request, else profile default, else the global per-delegation ceiling.
//
// 0 means uncapped at this level — the per-tree budget still applies, so
// "uncapped" never means "unbounded".
func EffectiveMaxTokens(requested int, profile *entity.AgentProfile, lim Limits) int {
	lim = lim.normalize()
	n := requested
	if n <= 0 && profile != nil {
		n = profile.DefaultMaxTokens
	}
	if n <= 0 {
		n = lim.MaxTokensPerDelegation
	}
	if lim.MaxTokensPerDelegation > 0 && n > lim.MaxTokensPerDelegation {
		n = lim.MaxTokensPerDelegation
	}
	return n
}

// Admit runs every pre-spawn brake for one prospective delegation.
//
// Order is intentional: the cheap in-memory checks (kill-switch, depth,
// cycle) run before the two that need a query. A tree that is already
// too deep should not cost a database round-trip to reject.
//
// Returns a *Refusal when the delegation must not start.
func (r *Repo) Admit(
	ctx context.Context,
	lim Limits,
	profile *entity.AgentProfile,
	rootID string,
	depth int,
	ancestorKeys []string,
) error {
	lim = lim.normalize()

	if lim.Disabled {
		return &Refusal{
			Reason:  RefusedDisabled,
			Message: "Sub-agent delegation is currently disabled by an administrator.",
		}
	}
	if profile == nil || profile.Disabled {
		return &Refusal{
			Reason:  RefusedProfileGone,
			Message: "That agent profile is disabled.",
		}
	}
	if depth > lim.MaxDepth {
		return &Refusal{
			Reason: RefusedDepth,
			Message: fmt.Sprintf(
				"Delegation depth %d exceeds the limit of %d. Do this part of the work yourself instead of delegating further.",
				depth, lim.MaxDepth),
		}
	}
	// Cycle guard: refuse when this profile already appears in the chain
	// from root to parent. Without it, two profiles that delegate to each
	// other spawn until some other limit happens to catch them.
	for _, k := range ancestorKeys {
		if k == profile.Key {
			return &Refusal{
				Reason: RefusedCycle,
				Message: fmt.Sprintf(
					"Profile %q is already active higher up this delegation chain; delegating to it again would loop.",
					profile.Key),
			}
		}
	}

	// Root-scoped checks. A depth-0 delegation has no existing tree, so
	// skip the queries entirely.
	if rootID == "" {
		return nil
	}

	used, err := r.SumTurnsByRoot(ctx, rootID)
	if err != nil {
		return err
	}
	if used >= lim.RootBudget {
		return &Refusal{
			Reason: RefusedBudget,
			Message: fmt.Sprintf(
				"This delegation tree has used its whole turn budget (%d/%d). Already-running sub-agents will finish, but no new ones can start. Summarise with what you have.",
				used, lim.RootBudget),
		}
	}

	if lim.RootTokenBudget > 0 {
		spent, err := r.SumTokensByRoot(ctx, rootID)
		if err != nil {
			return err
		}
		if spent >= lim.RootTokenBudget {
			return &Refusal{
				Reason: RefusedTokenBudget,
				Message: fmt.Sprintf(
					"This delegation tree has spent its whole token budget (%d/%d). Running sub-agents will finish, but no new ones can start. Summarise with what you have.",
					spent, lim.RootTokenBudget),
			}
		}
	}

	// The parallel cap is deliberately NOT checked here. Depth, cycle and
	// budget mean "this must not happen"; a full room means "not yet", and
	// the answer to "not yet" is a queue, not a rejection. Run enqueues
	// instead — see queue.go. Refusing here would throw away three
	// quarters of a four-way fan-out.
	return nil
}

// AdmitMessage decides whether one more agent-to-agent message may be
// sent, given the tree's current hop count.
//
// Hitting the cap stops MESSAGES, not the agents: every instance stays
// addressable and its work stands, so a human can allow more hops and the
// conversation resumes where it left off. Killing the tree here would
// punish the work for the chatter.
func AdmitMessage(lim Limits, hop int) error {
	lim = lim.normalize()
	if lim.Disabled {
		return &Refusal{
			Reason:  RefusedDisabled,
			Message: "Sub-agent delegation is currently disabled by an administrator.",
		}
	}
	if hop >= lim.MaxHops {
		return &Refusal{
			Reason: RefusedHops,
			Message: fmt.Sprintf(
				"Agents have exchanged %d messages since the last human turn (limit %d). "+
					"Stop messaging, summarise what you have, and report back to the user.",
				hop, lim.MaxHops),
		}
	}
	return nil
}
