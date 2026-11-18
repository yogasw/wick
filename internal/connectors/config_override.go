package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// Per-call config override: a saved connector keeps its config, but one
// call may run with a few keys set differently (e.g. mode = write for a
// single resolve). The instance's admin or owner lists which keys may be
// overridden (none by default). Only choice fields qualify (a dropdown or
// a yes/no): their values are fixed by the connector, so an override can
// never point the instance's credentials at another host. An agent's
// call with an override runs at once when the person it works for may
// configure the instance; otherwise it needs an approval (or a rule) from
// someone who may.

// ConfigOverrideParam is the reserved parameter an access request carries
// the override in, so the card shows it and the approval covers it.
const ConfigOverrideParam = "config_override"

// OwnerCheck reports whether userID owns connectorID (its owner tag).
type OwnerCheck func(ctx context.Context, userID, connectorID string) bool

// SetOwnerCheck wires the instance-owner lookup. Set once at boot.
func (s *Service) SetOwnerCheck(f OwnerCheck) { s.ownerCheck = f }

// overridableField reports whether a config field may ever be listed.
func overridableField(c entity.Config) bool {
	if c.IsSecret || c.Internal || c.Hidden {
		return false
	}
	switch c.Type {
	case "dropdown":
		return strings.TrimSpace(c.Options) != ""
	case "bool", "boolean", "checkbox":
		return true
	}
	return false
}

// OverridableCandidates lists the config keys of a connector type that an
// admin may allow to be overridden per call.
func (s *Service) OverridableCandidates(key string) []string {
	mod, ok := s.Module(key)
	if !ok {
		return nil
	}
	var out []string
	for _, c := range mod.Configs {
		if overridableField(c) {
			out = append(out, c.Key)
		}
	}
	return out
}

// OverridableKeys is the instance's allowed list, minus anything that no
// longer qualifies.
func (s *Service) OverridableKeys(c entity.Connector) []string {
	if strings.TrimSpace(c.OverridableConfig) == "" {
		return nil
	}
	ok := map[string]bool{}
	for _, k := range s.OverridableCandidates(c.Key) {
		ok[k] = true
	}
	var out []string
	for _, k := range strings.Split(c.OverridableConfig, ",") {
		if k = strings.TrimSpace(k); ok[k] && !contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// SetOverridableConfig saves the keys one instance lets callers override
// per call. Unknown or ineligible keys are refused.
func (s *Service) SetOverridableConfig(ctx context.Context, id string, keys []string) ([]string, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("connector %q not found", id)
	}
	allowed := map[string]bool{}
	for _, k := range s.OverridableCandidates(c.Key) {
		allowed[k] = true
	}
	var out []string
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || contains(out, k) {
			continue
		}
		if !allowed[k] {
			return nil, fmt.Errorf("config %q cannot be overridden per call (only choice fields that are not secret)", k)
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out, s.repo.SetOverridableConfig(ctx, id, strings.Join(out, ","))
}

// checkOverride validates an override against the instance: every key
// listed as overridable and every value one of the field's choices.
func (s *Service) checkOverride(mod connector.Module, c entity.Connector, ov map[string]string) error {
	if len(ov) == 0 {
		return nil
	}
	allowed := s.OverridableKeys(c)
	if len(allowed) == 0 {
		return fmt.Errorf("connector %q does not allow a config override (its admin or owner can list keys under Access policy)", c.Label)
	}
	fields := map[string]entity.Config{}
	for _, f := range mod.Configs {
		fields[f.Key] = f
	}
	for k, v := range ov {
		if !contains(allowed, k) {
			return fmt.Errorf("config %q cannot be overridden on connector %q (allowed: %s)", k, c.Label, strings.Join(allowed, ", "))
		}
		f := fields[k]
		switch f.Type {
		case "bool", "boolean", "checkbox":
			if v != "true" && v != "false" {
				return fmt.Errorf("config %q takes true or false", k)
			}
		default:
			opts := strings.Split(f.Options, "|")
			hit := false
			for _, o := range opts {
				if strings.TrimSpace(o) == v {
					hit = true
					break
				}
			}
			if !hit {
				return fmt.Errorf("config %q takes one of: %s", k, strings.Join(opts, ", "))
			}
		}
	}
	return nil
}

// CanConfigure reports whether userID may change connectorID's config:
// an admin, the instance's owner, or anyone who reaches it when the
// instance lets others configure it.
func (s *Service) CanConfigure(ctx context.Context, userID, connectorID string) bool {
	if s.identity == nil || userID == "" {
		return false
	}
	u, tags, err := s.identity(ctx, userID)
	if err != nil || u == nil {
		return false
	}
	if u.IsAdmin() {
		return true
	}
	c, err := s.repo.Get(ctx, connectorID)
	if err != nil {
		return false
	}
	if c.CreatedBy == u.ID || (s.ownerCheck != nil && s.ownerCheck(ctx, u.ID, c.ID)) {
		return true
	}
	if !c.AllowOthersConfigure {
		return false
	}
	vis, err := s.isVisibleDirect(WithoutAgentScope(ctx), c.ID, u.ID, tags, false)
	return err == nil && vis
}

// OverrideFromParams pulls the reserved override parameter out of an
// action's params (as an object or a JSON string). The rest is returned
// as is.
func OverrideFromParams(params map[string]any) (map[string]string, map[string]any, error) {
	raw, ok := params[ConfigOverrideParam]
	if !ok {
		return nil, params, nil
	}
	rest := make(map[string]any, len(params))
	for k, v := range params {
		if k != ConfigOverrideParam {
			rest[k] = v
		}
	}
	ov, err := ParseOverride(raw)
	return ov, rest, err
}

// ParseOverride reads an override given as an object or a JSON string.
func ParseOverride(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	var m map[string]any
	switch x := raw.(type) {
	case map[string]any:
		m = x
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, v := range x {
			out[k] = v
		}
		return nonEmpty(out), nil
	case string:
		if strings.TrimSpace(x) == "" {
			return nil, nil
		}
		if err := json.Unmarshal([]byte(x), &m); err != nil {
			return nil, fmt.Errorf("%s must be an object like {\"mode\": \"write\"}", ConfigOverrideParam)
		}
	default:
		return nil, fmt.Errorf("%s must be an object like {\"mode\": \"write\"}", ConfigOverrideParam)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = stringifyParam(v)
	}
	return nonEmpty(out), nil
}

func nonEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// overrideOpens reports whether op, switched off by the stored config, is
// on once the override is applied.
func overrideOpens(mod connector.Module, stored, ov map[string]string, op connector.Operation) bool {
	vals := connector.EffectiveConfigValues(mod, stored)
	for k, v := range ov {
		vals[k] = v
	}
	return connector.OpVisible(op, vals)
}

// configOffReasonFor is ConfigOffReason for one saved instance: when the
// key the op needs may be overridden, it says how to ask for that.
func (s *Service) configOffReasonFor(c entity.Connector, op connector.Operation) string {
	field, values := configOffNeed(op)
	if contains(s.OverridableKeys(c), field) {
		first, _, _ := strings.Cut(values, "|")
		return fmt.Sprintf("turned off by this connector's config (needs %s = %s); this instance allows that key to be overridden for one call: add %s {%q: %q} to the params. It runs at once when the person you work for may configure this connector; otherwise ask with wick_request_access (same params) and someone who may configure it approves", field, values, ConfigOverrideParam, field, first)
	}
	return ConfigOffReason(op)
}

func contains(list []string, x string) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}
