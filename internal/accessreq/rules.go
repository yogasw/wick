package accessreq

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/yogasw/wick/internal/entity"
)

// Rule effects.
const (
	EffectDeny        = "deny"
	EffectRequire     = "require"
	EffectAutoApprove = "auto_approve"
	// EffectApprovers decides WHO may approve a matching action, by
	// script: "add" lets in anyone the script allows (on top of admins
	// and owners), "only" makes every approver pass it too.
	EffectApprovers = "approvers"
)

// Approver rule modes.
const (
	ApproversAdd  = "add"
	ApproversOnly = "only"
)

// RuleDoc is a rule as people write it (YAML or JSON):
//
//	name: staging-apps-auto
//	effect: auto_approve
//	when:
//	  ops: [apps.*, bots.*]   # <connector key or id>.<op>, globs
//	  kind: [read, write]
//	  agents: [captain]              # agent id or name, optional
//	  all:
//	    - fact: app.env
//	      equals: staging
type RuleDoc struct {
	Name     string `json:"name" yaml:"name"`
	Effect   string `json:"effect" yaml:"effect"`
	Simulate bool   `json:"simulate,omitempty" yaml:"simulate,omitempty"`
	Disabled bool   `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	// Approvers is the mode of an approvers rule: add (default) or only.
	Approvers string   `json:"approvers,omitempty" yaml:"approvers,omitempty"`
	When      RuleWhen `json:"when" yaml:"when"`
}

// RuleWhen is what an action must look like for the rule to match. Every
// listed part must hold; an empty part matches anything.
type RuleWhen struct {
	Ops    []string `json:"ops,omitempty" yaml:"ops,omitempty"`
	Kind   []string `json:"kind,omitempty" yaml:"kind,omitempty"`
	Agents []string `json:"agents,omitempty" yaml:"agents,omitempty"`
	All    []Cond   `json:"all,omitempty" yaml:"all,omitempty"`
	// Script is a Code Mode script (TypeScript) that decides the last
	// condition. It reads `input` and may call READ-ONLY connector ops
	// with wick.call; it returns true/false or {allow, reason}. It runs as
	// the rule's owner. An error or a timeout never auto-approves and
	// never lets an approver in.
	Script string `json:"script,omitempty" yaml:"script,omitempty"`
}

// MaxScriptBytes caps a rule script.
const MaxScriptBytes = 20000

// Cond tests one fact. Exactly one of Equals, NotEquals, In, Exists is
// set. A fact that cannot be determined fails every test except
// exists: false, so an unknown never auto-approves.
type Cond struct {
	Fact      string   `json:"fact" yaml:"fact"`
	Equals    *string  `json:"equals,omitempty" yaml:"equals,omitempty"`
	NotEquals *string  `json:"not_equals,omitempty" yaml:"not_equals,omitempty"`
	In        []string `json:"in,omitempty" yaml:"in,omitempty"`
	Exists    *bool    `json:"exists,omitempty" yaml:"exists,omitempty"`
}

// ParseRule reads YAML or JSON into a validated RuleDoc.
func ParseRule(raw string) (RuleDoc, error) {
	var d RuleDoc
	if strings.TrimSpace(raw) == "" {
		return d, fmt.Errorf("rule is empty")
	}
	if err := yaml.Unmarshal([]byte(raw), &d); err != nil {
		return d, fmt.Errorf("rule is not valid YAML or JSON: %w", err)
	}
	return d, d.Validate()
}

// Validate refuses a rule that is malformed or too wide to be safe.
func (d RuleDoc) Validate() error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch d.Effect {
	case EffectDeny, EffectRequire, EffectAutoApprove, EffectApprovers:
	default:
		return fmt.Errorf("effect must be deny, require, auto_approve or approvers")
	}
	if len(d.When.Script) > MaxScriptBytes {
		return fmt.Errorf("when.script is over %d bytes", MaxScriptBytes)
	}
	if d.Effect == EffectApprovers {
		if strings.TrimSpace(d.When.Script) == "" {
			return fmt.Errorf("an approvers rule needs when.script: it decides who may approve (input.viewer, input.action)")
		}
		switch d.Approvers {
		case "", ApproversAdd, ApproversOnly:
		default:
			return fmt.Errorf("approvers must be add or only")
		}
	} else if d.Approvers != "" {
		return fmt.Errorf("approvers is only for effect: approvers")
	}
	if len(d.When.Ops) == 0 {
		return fmt.Errorf("when.ops is required: name the operations, e.g. [notion.update_page] or [apps.*]")
	}
	for _, p := range d.When.Ops {
		if _, err := path.Match(strings.ToLower(p), "x.y"); err != nil {
			return fmt.Errorf("when.ops %q is not a valid pattern", p)
		}
	}
	for _, k := range d.When.Kind {
		switch k {
		case KindRead, KindWrite, KindSend, KindDelete:
		default:
			return fmt.Errorf("when.kind %q: use read, write, send or delete", k)
		}
	}
	if d.Effect == EffectAutoApprove {
		if len(d.When.Kind) == 0 {
			return fmt.Errorf("an auto_approve rule must list when.kind (delete is never auto-approved)")
		}
		for _, k := range d.When.Kind {
			if k == KindDelete {
				return fmt.Errorf("delete is never auto-approved: drop it from when.kind")
			}
		}
	}
	for i, c := range d.When.All {
		if strings.TrimSpace(c.Fact) == "" {
			return fmt.Errorf("when.all[%d]: fact is required", i)
		}
		set := 0
		if c.Equals != nil {
			set++
		}
		if c.NotEquals != nil {
			set++
		}
		if len(c.In) > 0 {
			set++
		}
		if c.Exists != nil {
			set++
		}
		if set != 1 {
			return fmt.Errorf("when.all[%d]: use exactly one of equals, not_equals, in, exists", i)
		}
	}
	return nil
}

// JSON renders the doc for storage.
func (d RuleDoc) JSON() string {
	b, _ := json.Marshal(d)
	return string(b)
}

// YAML renders the doc for editing.
func (d RuleDoc) YAML() string {
	b, err := yaml.Marshal(d)
	if err != nil {
		return d.JSON()
	}
	return string(b)
}

// Action is what rules and grants are tested against.
type Action struct {
	SessionID    string
	AgentID      string
	AgentName    string
	ConnectorID  string
	ConnectorKey string
	OpKey        string
	Kind         string
	Params       map[string]string
	// Config is the connector instance's own subject settings (app_code
	// of a connector bound to one app). Never credentials.
	Config map[string]string
}

// paramAliases are the parameter names a subject fact is read from, in
// order, so one rule covers connectors that name the same thing
// differently.
var paramAliases = map[string][]string{
	"app":     {"app_code", "app_id", "appCode", "appId", "appid", "app"},
	"channel": {"channel_id", "channel", "channelId", "conversation"},
	"repo":    {"repo", "repository", "repo_slug", "project"},
	"branch":  {"branch", "branch_name", "ref", "source_branch"},
}

// FactNames lists the facts rules can test, for the editor's help.
func FactNames() []string {
	return []string{
		"agent", "connector", "connector.key", "op", "kind",
		"app", "channel", "repo", "branch", "param.<name>",
		"app.env", "connector.env", "<app|channel|repo|branch|connector>.<attr> (from the facts table)",
	}
}

// subject is what a subject fact is about: the app (or channel, repo…)
// the action touches, from the op's parameters and from the connector's
// own config (a connector bound to one app carries its app_code there).
// When the two name different things the subject is unknown: a rule then
// never auto-approves, so an agent cannot point a parameter at a staging
// app while the connector itself writes to production.
func (a Action) subject(base string) (string, bool) {
	if base == "connector" {
		return a.ConnectorID, a.ConnectorID != ""
	}
	pick := func(src map[string]string) string {
		for _, name := range paramAliases[base] {
			if v := strings.TrimSpace(src[name]); v != "" {
				return v
			}
		}
		return ""
	}
	fromParams, fromConfig := pick(a.Params), pick(a.Config)
	switch {
	case fromParams != "" && fromConfig != "" && !strings.EqualFold(fromParams, fromConfig):
		return "", false
	case fromConfig != "":
		return fromConfig, true
	case fromParams != "":
		return fromParams, true
	}
	return "", false
}

// factValue resolves a fact for an action. Facts come from the action's
// real parameters and from the facts table people maintain, never from
// anything the agent says about the action.
func (s *Service) factValue(ctx context.Context, a Action, name string) (string, bool) {
	switch name {
	case "agent":
		return a.AgentID, a.AgentID != ""
	case "connector":
		return a.ConnectorID, true
	case "connector.key":
		return a.ConnectorKey, true
	case "op":
		return a.ConnectorKey + "." + a.OpKey, true
	case "kind":
		return a.Kind, true
	case "app", "channel", "repo", "branch":
		return a.subject(name)
	}
	if p, ok := strings.CutPrefix(name, "param."); ok {
		v, has := a.Params[p]
		return v, has
	}
	base, _, ok := strings.Cut(name, ".")
	if !ok || s.store == nil {
		return "", false
	}
	if _, known := paramAliases[base]; !known && base != "connector" {
		return "", false
	}
	subj, ok := a.subject(base)
	if !ok {
		return "", false
	}
	return s.store.fact(ctx, name, subj)
}

func (s *Service) condHolds(ctx context.Context, a Action, c Cond) bool {
	v, ok := s.factValue(ctx, a, c.Fact)
	if c.Exists != nil {
		return ok == *c.Exists
	}
	if !ok {
		return false
	}
	switch {
	case c.Equals != nil:
		return strings.EqualFold(v, *c.Equals)
	case c.NotEquals != nil:
		return !strings.EqualFold(v, *c.NotEquals)
	case len(c.In) > 0:
		for _, x := range c.In {
			if strings.EqualFold(v, x) {
				return true
			}
		}
	}
	return false
}

func opMatches(patterns []string, a Action) bool {
	if len(patterns) == 0 {
		return true
	}
	names := []string{
		strings.ToLower(a.ConnectorKey + "." + a.OpKey),
		strings.ToLower(a.ConnectorID + "." + a.OpKey),
	}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "*" {
			return true
		}
		for _, n := range names {
			if ok, _ := path.Match(p, n); ok {
				return true
			}
		}
	}
	return false
}

func agentMatches(list []string, a Action) bool {
	if len(list) == 0 {
		return true
	}
	for _, x := range list {
		x = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(x)), "@")
		if x != "" && (x == strings.ToLower(a.AgentID) || x == strings.TrimPrefix(strings.ToLower(a.AgentName), "@")) {
			return true
		}
	}
	return false
}

func kindMatches(list []string, kind string) bool {
	if len(list) == 0 {
		return true
	}
	for _, k := range list {
		if k == kind {
			return true
		}
	}
	return false
}

// Matches reports whether the rule matches the action.
func (s *Service) Matches(ctx context.Context, d RuleDoc, a Action) bool {
	if !opMatches(d.When.Ops, a) || !kindMatches(d.When.Kind, a.Kind) || !agentMatches(d.When.Agents, a) {
		return false
	}
	for _, c := range d.When.All {
		if !s.condHolds(ctx, a, c) {
			return false
		}
	}
	return true
}

// Verdict is what the rules say about an action.
type Verdict struct {
	// Effect is deny, require, auto_approve or "" when no rule matched.
	Effect string
	// Rule is the rule that decided.
	Rule *entity.AccessRule
	// Simulated names the simulate-mode rules that would have
	// auto-approved.
	Simulated []string
}

// ruleApplies: an admin's rule covers every session; anyone else's only
// the sessions and agents they own, so nobody can loosen (or tighten)
// someone else's agent with their own rule.
func (s *Service) ruleApplies(ctx context.Context, r entity.AccessRule, sessionOwner, agentOwner string) bool {
	if r.OwnerID == "" {
		return false
	}
	if r.OwnerID == sessionOwner || (agentOwner != "" && r.OwnerID == agentOwner) {
		return true
	}
	if s.dir == nil {
		return false
	}
	p, ok := s.dir.User(ctx, r.OwnerID)
	return ok && p.IsAdmin && p.Approved
}

// Evaluate runs the rules over an action: deny beats require beats
// auto_approve. An auto_approve rule only acts when its owner can run
// the action themselves; otherwise it is skipped (the card still shows).
func (s *Service) Evaluate(ctx context.Context, a Action) Verdict {
	var v Verdict
	if s.store == nil {
		return v
	}
	rules, err := s.store.rules(ctx)
	if err != nil || len(rules) == 0 {
		return v
	}
	sessionOwner, agentOwner := s.owners(ctx, a.SessionID, a.AgentID)
	var deny, require, auto *entity.AccessRule
	for i := range rules {
		r := rules[i]
		if r.Disabled || !s.ruleApplies(ctx, r, sessionOwner, agentOwner) {
			continue
		}
		if r.Effect == EffectApprovers {
			continue
		}
		d, err := ParseRule(r.Doc)
		if err != nil || !s.Matches(ctx, d, a) {
			continue
		}
		if strings.TrimSpace(d.When.Script) != "" {
			res := s.runRuleScript(ctx, r, d.When.Script, scriptInput(a, nil))
			switch {
			case res.Err != "" && r.Effect == EffectDeny:
				// A deny rule whose check failed still asks a person.
				if require == nil {
					require = &rules[i]
				}
				continue
			case res.Err != "" || !res.Allow:
				continue
			}
		}
		switch r.Effect {
		case EffectDeny:
			if deny == nil {
				deny = &rules[i]
			}
		case EffectRequire:
			if require == nil {
				require = &rules[i]
			}
		case EffectAutoApprove:
			if r.Simulate {
				v.Simulated = append(v.Simulated, r.Name)
				continue
			}
			if auto != nil || s.cat == nil {
				continue
			}
			if ok, _ := s.cat.CanRun(ctx, r.OwnerID, a.ConnectorID, a.OpKey); ok {
				auto = &rules[i]
			}
		}
	}
	switch {
	case deny != nil:
		v.Effect, v.Rule = EffectDeny, deny
	case require != nil:
		v.Effect, v.Rule = EffectRequire, require
	case auto != nil:
		v.Effect, v.Rule = EffectAutoApprove, auto
	}
	return v
}
