package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/userconfig"
)

// Failover config: the per-instance chain a turn moves along when its
// provider hits a usage limit, an auth error or an outage. Only the
// config lives here — validation, defaults and the persisted mirror. The
// chain always belongs to the HOME instance of a session, so a target's
// own chain is never followed (no A→B→A loops).

// Failover triggers: the error kinds a chain reacts to.
const (
	FailoverTriggerLimit       = "limit"
	FailoverTriggerAuth        = "auth"
	FailoverTriggerUnavailable = "unavailable"
)

// FailoverTriggers lists every trigger, in the order the UI shows them.
var FailoverTriggers = []string{FailoverTriggerLimit, FailoverTriggerAuth, FailoverTriggerUnavailable}

// MaxFailoverTargets caps a chain: each hop is a full re-spawn, so a long
// chain only delays the error the user eventually sees.
const MaxFailoverTargets = 6

// Recheck window bounds, in minutes (one hour at most).
const (
	MinFailoverRecheckMinutes     = 1
	MaxFailoverRecheckMinutes     = 60
	DefaultFailoverRecheckMinutes = 10
)

// FailoverTarget is one step of a chain. Instance is the "type/name" tag
// (see InstanceTag); ModelID is the model pin the target spawns with, in
// the same form a session's ModelID takes — flat or a grouped
// "<path>@<model>" pin (EncodePin), so a sub-model / account rides along.
// "" = the target's default model.
type FailoverTarget struct {
	Instance string
	ModelID  string
}

// FailoverConfig is one instance's failover settings.
type FailoverConfig struct {
	Enabled bool
	// Triggers is a subset of FailoverTriggers.
	Triggers []string
	// Chain is tried in order, at most MaxFailoverTargets long.
	Chain []FailoverTarget
	// RecheckMinutes is how often an unhealthy instance is probed again.
	RecheckMinutes int
	// ReturnHome moves the session back to its home instance once that
	// recovers, at the next turn boundary.
	ReturnHome bool
}

// DefaultFailover is the config of an instance that never saved one:
// off, every trigger ticked, a ten-minute recheck, return home on.
func DefaultFailover() FailoverConfig {
	return FailoverConfig{
		Triggers:       append([]string(nil), FailoverTriggers...),
		RecheckMinutes: DefaultFailoverRecheckMinutes,
		ReturnHome:     true,
	}
}

// EffectiveFailover is ins's stored config, or DefaultFailover when it
// has none. The result shares no slices with ins.
func EffectiveFailover(ins Instance) FailoverConfig {
	if ins.Failover == nil {
		return DefaultFailover()
	}
	return cloneFailover(*ins.Failover)
}

// HasTrigger reports whether cfg reacts to trigger.
func (c FailoverConfig) HasTrigger(trigger string) bool {
	for _, t := range c.Triggers {
		if t == trigger {
			return true
		}
	}
	return false
}

// InstanceTag is the canonical "type/name" tag of an instance. A chain
// always stores this form, even for a default instance (name == type),
// so a tag compares by plain string equality.
func InstanceTag(t Type, name string) string {
	if name == "" {
		name = string(t)
	}
	return string(t) + "/" + name
}

// SplitInstanceTag splits "type/name" (or a bare "type", the default
// instance) into its parts.
func SplitInstanceTag(tag string) (Type, string) {
	tag = strings.TrimSpace(tag)
	if i := strings.IndexByte(tag, '/'); i >= 0 {
		return Type(tag[:i]), tag[i+1:]
	}
	return Type(tag), tag
}

// NormalizeFailover tidies what an editor sent: trims, lower-cases and
// de-duplicates the triggers, canonicalizes target tags (bare "codex" →
// "codex/codex", the type lower-cased), drops rows with no instance and
// reads an unset (0) recheck interval as the default. It never rejects —
// ValidateFailover does.
func NormalizeFailover(cfg FailoverConfig) FailoverConfig {
	out := FailoverConfig{Enabled: cfg.Enabled, RecheckMinutes: cfg.RecheckMinutes, ReturnHome: cfg.ReturnHome}
	if out.RecheckMinutes == 0 {
		out.RecheckMinutes = DefaultFailoverRecheckMinutes
	}
	seen := map[string]bool{}
	for _, t := range cfg.Triggers {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out.Triggers = append(out.Triggers, t)
	}
	for _, tg := range cfg.Chain {
		if strings.TrimSpace(tg.Instance) == "" {
			continue
		}
		typ, name := SplitInstanceTag(tg.Instance)
		typ = Type(strings.ToLower(string(typ)))
		out.Chain = append(out.Chain, FailoverTarget{Instance: InstanceTag(typ, name), ModelID: strings.TrimSpace(tg.ModelID)})
	}
	return out
}

// ValidateFailover checks cfg as the chain of self against the configured
// instances all. Every problem is reported (errors.Join), nil = valid.
//
// Refused: a target of an unsupported type, a target that is self, a
// target listed twice, more than
// MaxFailoverTargets targets, an unknown trigger, no trigger while
// enabled, and RecheckMinutes outside 1..60. An unknown or disabled
// target is refused only while the chain is enabled, so an instance that
// was removed later never blocks switching the chain off.
//
// A target of another provider type is NOT refused: history does not
// carry over, which FailoverWarnings reports instead.
func ValidateFailover(self Instance, cfg FailoverConfig, all []Instance) error {
	var errs []error
	for _, t := range cfg.Triggers {
		if !isFailoverTrigger(t) {
			errs = append(errs, fmt.Errorf("unknown trigger %q (want one of %s)", t, strings.Join(FailoverTriggers, ", ")))
		}
	}
	if cfg.Enabled && len(cfg.Triggers) == 0 {
		errs = append(errs, errors.New("pick at least one trigger"))
	}
	if cfg.RecheckMinutes < MinFailoverRecheckMinutes || cfg.RecheckMinutes > MaxFailoverRecheckMinutes {
		errs = append(errs, fmt.Errorf("recheck interval must be %d..%d minutes, got %d", MinFailoverRecheckMinutes, MaxFailoverRecheckMinutes, cfg.RecheckMinutes))
	}
	if len(cfg.Chain) > MaxFailoverTargets {
		errs = append(errs, fmt.Errorf("at most %d failover targets, got %d", MaxFailoverTargets, len(cfg.Chain)))
	}
	selfTag := InstanceTag(self.Type, self.Name)
	seen := map[string]bool{}
	for _, tg := range cfg.Chain {
		typ, name := SplitInstanceTag(tg.Instance)
		tag := InstanceTag(typ, name)
		switch {
		case strings.TrimSpace(tg.Instance) == "":
			errs = append(errs, errors.New("a failover target has no instance"))
			continue
		case !isSupported(typ):
			errs = append(errs, fmt.Errorf("unknown provider instance %s", tag))
			continue
		case tag == selfTag:
			errs = append(errs, fmt.Errorf("%s cannot fail over to itself", tag))
			continue
		case seen[tag]:
			errs = append(errs, fmt.Errorf("%s is listed more than once", tag))
			continue
		}
		seen[tag] = true
		if !cfg.Enabled {
			continue
		}
		ins, ok := failoverFindIn(all, typ, name)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("unknown provider instance %s", tag))
		case ins.Disabled:
			errs = append(errs, fmt.Errorf("provider instance %s is disabled", tag))
		}
	}
	return errors.Join(errs...)
}

// FailoverWarning is a cost of one valid target, shown next to it.
type FailoverWarning struct {
	Instance string // the target's "type/name" tag
	Message  string
}

// FailoverWarnings lists what a valid chain still costs, per target:
//   - another provider type than self: the CLI conversation history stays
//     behind (wick carries the last message and a short summary only);
//   - a target sharing self's login (AuthFrom either way, or both
//     borrowing the same one): the same limit or auth error hits it too;
//   - a model pin the target does not offer (checked where that is known
//     offline: grouped pins through ResolvePin, flat ids against a curated
//     model list). A stale pin falls back to the default at spawn.
//
// Targets missing from all only get the type warning; ValidateFailover
// reports them.
func FailoverWarnings(self Instance, cfg FailoverConfig, all []Instance) []FailoverWarning {
	var out []FailoverWarning
	for _, tg := range cfg.Chain {
		typ, name := SplitInstanceTag(tg.Instance)
		if typ == "" {
			continue
		}
		tag := InstanceTag(typ, name)
		if typ != self.Type {
			out = append(out, FailoverWarning{Instance: tag, Message: "history does not carry over to " + string(typ)})
		}
		ins, ok := failoverFindIn(all, typ, name)
		if !ok {
			continue
		}
		if sharesLogin(self, ins) {
			out = append(out, FailoverWarning{Instance: tag, Message: "shares the login of " + InstanceTag(self.Type, self.Name) + ", so the same limit or auth error hits it too"})
		}
		if !modelPinOffered(ins, tg.ModelID) {
			out = append(out, FailoverWarning{Instance: tag, Message: "model " + tg.ModelID + " is not offered by " + tag + "; its default model runs instead"})
		}
	}
	return out
}

// sharesLogin reports whether a and b run on the same login (authshare.go).
func sharesLogin(a, b Instance) bool {
	if a.Type != b.Type {
		return false
	}
	return b.AuthFrom == a.Name || a.AuthFrom == b.Name || (a.AuthFrom != "" && a.AuthFrom == b.AuthFrom)
}

// modelPinOffered reports whether ins offers pin, true whenever that
// cannot be told offline (no pin, a live model list, no curated list).
func modelPinOffered(ins Instance, pin string) bool {
	if pin == "" {
		return true
	}
	if _, _, grouped := DecodePin(pin); grouped {
		if _, ok := ModelSetsFor(ins.Type); !ok {
			return true
		}
		_, ok := ResolvePin(&ins, pin)
		return ok
	}
	if ins.LiveModels || len(ins.Models) == 0 {
		return true
	}
	for _, m := range ins.Models {
		if m.ID == pin {
			return true
		}
	}
	return false
}

func isFailoverTrigger(t string) bool {
	for _, k := range FailoverTriggers {
		if k == t {
			return true
		}
	}
	return false
}

func failoverFindIn(all []Instance, t Type, name string) (Instance, bool) {
	for _, ins := range all {
		if ins.Type == t && ins.Name == name {
			return ins, true
		}
	}
	return Instance{}, false
}

func cloneFailover(c FailoverConfig) FailoverConfig {
	c.Triggers = append([]string(nil), c.Triggers...)
	c.Chain = append([]FailoverTarget(nil), c.Chain...)
	return c
}

// ── persisted mirror ──────────────────────────────────────────────────
//
// nil-in → nil-out, like the other converters: an instance that never
// saved a chain keeps reading DefaultFailover.

func failoverFromUser(in *userconfig.FailoverConfig) *FailoverConfig {
	if in == nil {
		return nil
	}
	out := &FailoverConfig{
		Enabled:        in.Enabled,
		Triggers:       append([]string(nil), in.Triggers...),
		RecheckMinutes: in.RecheckMinutes,
		ReturnHome:     in.ReturnHome,
	}
	for _, t := range in.Chain {
		out.Chain = append(out.Chain, FailoverTarget{Instance: t.Instance, ModelID: t.ModelID})
	}
	return out
}

func failoverToUser(in *FailoverConfig) *userconfig.FailoverConfig {
	if in == nil {
		return nil
	}
	out := &userconfig.FailoverConfig{
		Enabled:        in.Enabled,
		Triggers:       append([]string(nil), in.Triggers...),
		RecheckMinutes: in.RecheckMinutes,
		ReturnHome:     in.ReturnHome,
	}
	for _, t := range in.Chain {
		out.Chain = append(out.Chain, userconfig.FailoverTarget{Instance: t.Instance, ModelID: t.ModelID})
	}
	return out
}

// renameFailoverTargets points every chain target oldTag at newTag, across
// all provider types: a chain may name an instance of any type.
func renameFailoverTargets(c *userconfig.ProvidersConfig, oldTag, newTag string) {
	for _, t := range SupportedTypes() {
		list := pickList(c, t)
		if list == nil {
			continue
		}
		for i := range *list {
			fo := (*list)[i].Failover
			if fo == nil {
				continue
			}
			for j := range fo.Chain {
				typ, name := SplitInstanceTag(fo.Chain[j].Instance)
				if InstanceTag(typ, name) == oldTag {
					fo.Chain[j].Instance = newTag
				}
			}
		}
	}
}

// pruneFailoverTargets drops target tag from every chain, across all
// provider types: a deleted instance must not linger in a chain, where a
// later instance re-created under the same name would silently inherit it.
func pruneFailoverTargets(c *userconfig.ProvidersConfig, tag string) {
	for _, t := range SupportedTypes() {
		list := pickList(c, t)
		if list == nil {
			continue
		}
		for i := range *list {
			fo := (*list)[i].Failover
			if fo == nil {
				continue
			}
			kept := fo.Chain[:0]
			for _, tg := range fo.Chain {
				typ, name := SplitInstanceTag(tg.Instance)
				if InstanceTag(typ, name) != tag {
					kept = append(kept, tg)
				}
			}
			fo.Chain = kept
		}
	}
}
