package provider

import (
	"context"
	"strings"
)

// modelpick.go: which model an omp/opencode spawn names when the session
// pins none, and whether a pin still fits the instance it now runs on.
//
// ProvenDefaultModel (modelwatch.go) stays the rule for a live instance
// whose Default model is empty: without evidence the CLI's own default
// stands. A Default model the operator chose is different — it is a choice,
// not a guess — so, like opencode's instanceDefault, it is always sent.

// liveDefaultFn is LiveDefaultModel; swapped in tests (it may exec the CLI).
var liveDefaultFn = LiveDefaultModel

// ExplicitLiveDefault is the model a live instance with an operator-chosen
// Default model runs when nothing is pinned: that model while the CLI lists
// it and the account was not refused it, else the next pickLiveDefault
// choice. "" when live mode is off or no Default model is set.
func ExplicitLiveDefault(ctx context.Context, ins Instance) string {
	if !LiveModelsEnabled(ins) || strings.TrimSpace(ins.LiveModelDefault) == "" {
		return ""
	}
	return liveDefaultFn(ctx, ins)
}

// InstanceOwnModel is ins's own model for a turn that must not inherit a
// model from elsewhere (a transcript another account wrote): the live
// default in live mode, else the model that last worked on the account,
// else the first chat model the CLI lists. "" when none is known.
func InstanceOwnModel(ctx context.Context, ins Instance) string {
	if LiveModelsEnabled(ins) {
		return liveDefaultFn(ctx, ins)
	}
	if m := LastWorkedModel(ins, ""); m != "" {
		return m
	}
	if list, _, _ := CachedCLIModels(ctx, ins, false); len(list) > 0 {
		return list[0].ID
	}
	return ""
}

// StalePin reports whether a session's model pin cannot run on ins — the
// account was refused that model, or ins's live list (cached, never
// fetched here) does not offer it — with the CLI model it names. Only
// omp/opencode; a pin ModelArgs already drops (wick-shaped, AI router)
// is not stale, it is ignored.
func StalePin(ins *Instance, pin string) (string, bool) {
	if ins == nil || pin == "" || ins.UseAIRouter || !SupportsAutoRetryModel(ins.Type) {
		return "", false
	}
	m := pin
	if p, ok := ResolvePin(ins, pin); ok {
		m = p.Model
	} else if isForeignModelPin(pin) {
		return "", false
	}
	for _, acct := range []string{modelPrefix(m), ""} {
		if _, bad := ModelUnavailable(*ins, acct, m); bad {
			return m, true
		}
	}
	if LiveModelsEnabled(*ins) {
		if list := cachedCLIModelsOnly(*ins); len(list) > 0 {
			for _, s := range list {
				if s.ID == m {
					return m, false
				}
			}
			return m, true
		}
	}
	return m, false
}

// cachedCLIModelsOnly is the cached CLI list for ins, nil when cold; it
// never starts a fetch.
func cachedCLIModelsOnly(ins Instance) []ModelSeed {
	cliModelsMu.Lock()
	defer cliModelsMu.Unlock()
	if e, ok := cliModelsLookup(ins, cliModelsKey(ins)); ok && e.err == nil {
		return e.models
	}
	return nil
}

// StalePinNotice is what the session reads when its pin is dropped.
func StalePinNotice(ins Instance, model string) string {
	return "Model " + model + " is not available on " + string(ins.Type) + "/" + ins.Name +
		", so this turn runs on the instance's default model instead."
}
