package connector

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/entity"
)

// ValidateModule checks the parts of a Module the type system cannot:
// today, every Operation.VisibleWhen predicate. A predicate must read
// "<field>:<a|b|c>", name a field declared in mod.Configs that is not a
// secret, and list at least one non-empty value. entity.VisibleWhenMatches
// treats a predicate without ':' as always-visible, so a typo would
// otherwise silently show the op everywhere.
//
// Called by the plugin --dump-manifest path and the plugin installer; the
// built-in registry is covered by a test that walks every module.
func ValidateModule(mod Module) error {
	configs := make(map[string]entity.Config, len(mod.Configs))
	for _, c := range mod.Configs {
		configs[c.Key] = c
	}
	for _, op := range mod.AllOps() {
		if op.VisibleWhen == "" {
			continue
		}
		key, values, ok := strings.Cut(op.VisibleWhen, ":")
		if !ok {
			return fmt.Errorf("connector %q op %q: visible_when %q must be <field>:<a|b|c>", mod.Meta.Key, op.Key, op.VisibleWhen)
		}
		key = strings.TrimSpace(key)
		cfg, found := configs[key]
		if !found {
			return fmt.Errorf("connector %q op %q: visible_when %q names unknown config field %q", mod.Meta.Key, op.Key, op.VisibleWhen, key)
		}
		if cfg.IsSecret {
			return fmt.Errorf("connector %q op %q: visible_when %q must not depend on secret field %q", mod.Meta.Key, op.Key, op.VisibleWhen, key)
		}
		hasValue := false
		for _, v := range strings.Split(values, "|") {
			if strings.TrimSpace(v) != "" {
				hasValue = true
				break
			}
		}
		if !hasValue {
			return fmt.Errorf("connector %q op %q: visible_when %q lists no value", mod.Meta.Key, op.Key, op.VisibleWhen)
		}
	}
	return nil
}

// EffectiveConfigValues is the instance config an op's VisibleWhen is
// evaluated against: every declared field's default (mod.Configs[i].Value)
// overlaid with what the instance has stored. Stored keys win even when
// empty — an explicitly saved "" is a real value. Defaults matter because a
// field added in a newer module version is not seeded for older rows yet.
func EffectiveConfigValues(mod Module, stored map[string]string) map[string]string {
	vals := make(map[string]string, len(mod.Configs)+len(stored))
	for _, c := range mod.Configs {
		vals[c.Key] = c.Value
	}
	for k, v := range stored {
		vals[k] = v
	}
	return vals
}

// OpVisible reports whether op's VisibleWhen holds for vals (normally the
// output of EffectiveConfigValues). An op without VisibleWhen is visible.
func OpVisible(op Operation, vals map[string]string) bool {
	return entity.VisibleWhenMatches(op.VisibleWhen, vals)
}
