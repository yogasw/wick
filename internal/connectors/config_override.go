package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// Per-call config override: a saved connector keeps its config, but one
// call may run with a few keys set differently (e.g. mode = write for a
// single resolve). Only choice fields qualify (a dropdown or a yes/no that
// is not secret): their values are fixed by the connector, so an override
// can never point the instance's credentials at another host. For an
// agent it is part of the access-request feature, which each project
// turns on (off by default): with it off nothing changes. With it on, an
// agent's override always waits for a card that someone who may configure
// the instance approves (or a rule they wrote).

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

// AccessGate reports whether the access-request feature is on for a
// session (its project turned it on).
type AccessGate func(sessionID string) bool

// SetAccessGate wires the per-project switch. Set once at boot; nil
// means off everywhere.
func (s *Service) SetAccessGate(f AccessGate) { s.accessGate = f }

// accessOn reports whether access requests (cards, rules, overrides) are
// on for this call's session.
func (s *Service) accessOn(sessionID string) bool {
	return sessionID != "" && s.accessGate != nil && s.accessGate(sessionID)
}

// checkOverride validates an override: every key a choice field that is
// not secret, every value one of the field's choices.
func (s *Service) checkOverride(mod connector.Module, c entity.Connector, ov map[string]string) error {
	if len(ov) == 0 {
		return nil
	}
	allowed := s.OverridableCandidates(c.Key)
	fields := map[string]entity.Config{}
	for _, f := range mod.Configs {
		fields[f.Key] = f
	}
	for k, v := range ov {
		if !contains(allowed, k) {
			if len(allowed) == 0 {
				return fmt.Errorf("connector %q has no config that can be overridden per call", c.Label)
			}
			return fmt.Errorf("config %q cannot be overridden on connector %q (only: %s)", k, c.Label, strings.Join(allowed, ", "))
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

// configOffReasonFor is ConfigOffReason for one saved instance: when
// access requests are on (on) and the key the op needs can be
// overridden, it says how.
func (s *Service) configOffReasonFor(c entity.Connector, op connector.Operation, on bool) string {
	field, values := configOffNeed(op)
	if on && contains(s.OverridableCandidates(c.Key), field) {
		first, _, _ := strings.Cut(values, "|")
		return fmt.Sprintf("turned off by this connector's config (needs %s = %s); it can be overridden for one call: ask with wick_request_access, adding %s {%q: %q} to the action's params; someone who may configure this connector approves it on the card", field, values, ConfigOverrideParam, field, first)
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

// requestHint ends a refusal with the way forward, only where access
// requests are on: elsewhere a refusal reads as it always did.
func (s *Service) requestHint(sessionID string) string {
	if s.accessOn(sessionID) {
		return " " + accessRequestHint
	}
	return ""
}
