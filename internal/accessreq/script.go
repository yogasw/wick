package accessreq

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// Rule scripts: a condition written as a Code Mode script, so a rule can
// look things up in other connectors (is the viewer a member of a channel,
// is the app code in a canvas, what does a report say) without wick
// growing a new kind of fact each time. Scripts live in the rule, are
// edited in the project settings and need no redeploy.
//
// The runner (wired by the server) runs the script as the rule's owner
// with READ-ONLY connector access: a write op refuses. The script reads
// `input` ({action, viewer}) and returns true / false or
// {allow: bool, reason: string}.

// ScriptRunner runs code as ownerID with read-only connector access and
// returns the script's JSON result and its log lines.
type ScriptRunner func(ctx context.Context, ownerID, code string, input map[string]any) (result json.RawMessage, logs []string, err error)

// ScriptResult is a script's verdict.
type ScriptResult struct {
	Allow  bool     `json:"allow"`
	Reason string   `json:"reason,omitempty"`
	Err    string   `json:"error,omitempty"`
	Logs   []string `json:"logs,omitempty"`
	Cached bool     `json:"cached,omitempty"`
}

const (
	scriptTimeout  = 10 * time.Second
	scriptCacheTTL = 5 * time.Minute
	scriptCacheMax = 2000
)

type scriptCache struct {
	mu sync.Mutex
	m  map[string]scriptCacheEntry
}

type scriptCacheEntry struct {
	res ScriptResult
	at  time.Time
}

func (c *scriptCache) get(key string, now time.Time) (ScriptResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) > scriptCacheTTL {
		return ScriptResult{}, false
	}
	return e.res, true
}

func (c *scriptCache) put(key string, res ScriptResult, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) >= scriptCacheMax {
		c.m = map[string]scriptCacheEntry{}
	}
	c.m[key] = scriptCacheEntry{res: res, at: now}
}

// SetScriptRunner wires the Code Mode runner for rule scripts. Without one
// a script condition never holds (fail closed).
func (s *Service) SetScriptRunner(r ScriptRunner) {
	s.mu.Lock()
	s.script = r
	s.mu.Unlock()
}

// scriptInput is what a rule script reads as `input`.
func scriptInput(a Action, viewer map[string]any) map[string]any {
	in := map[string]any{
		"action": map[string]any{
			"connector_id": a.ConnectorID, "connector_key": a.ConnectorKey, "op": a.OpKey, "kind": a.Kind,
			"params": nonNil(a.Params), "config": nonNil(a.Config),
			"agent_id": a.AgentID, "agent": strings.TrimPrefix(a.AgentName, "@"), "session_id": a.SessionID,
		},
	}
	// requester: the person whose message the agent was answering (null
	// when none: a schedule, another agent). Set by the channel, never by
	// the agent.
	if a.AskedBy != nil {
		in["requester"] = map[string]any{"id": a.AskedBy.ID, "name": a.AskedBy.Name, "channel": a.AskedBy.Channel, "wick_user_id": a.AskedBy.WickUserID}
	} else {
		in["requester"] = nil
	}
	if viewer != nil {
		in["viewer"] = viewer
	}
	return in
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// runRuleScript runs a rule's script, cached per rule revision and input.
func (s *Service) runRuleScript(ctx context.Context, r entity.AccessRule, code string, input map[string]any) ScriptResult {
	s.mu.RLock()
	run := s.script
	s.mu.RUnlock()
	if run == nil {
		return ScriptResult{Err: "rule scripts are not available on this wick"}
	}
	raw, _ := json.Marshal(input)
	sum := sha256.Sum256(append([]byte(r.ID+"|"+strconv.Itoa(r.Rev)+"|"), raw...))
	key := hex.EncodeToString(sum[:])
	now := s.now()
	if res, ok := s.cache.get(key, now); ok {
		res.Cached = true
		return res
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scriptTimeout)
	defer cancel()
	out, logs, err := run(sctx, r.OwnerID, code, input)
	res := parseScriptResult(out, err)
	res.Logs = logs
	if res.Err != "" {
		log.Warn().Str("rule", r.Name).Str("error", res.Err).Msg("accessreq: rule script failed (treated as not allowed)")
	} else {
		// Only a clean answer is cached; a failure is retried next time.
		s.cache.put(key, res, now)
	}
	return res
}

// parseScriptResult reads true/false or {allow, reason}. Anything else is
// an error: an unclear answer never allows.
func parseScriptResult(out json.RawMessage, err error) ScriptResult {
	if err != nil {
		return ScriptResult{Err: err.Error()}
	}
	switch strings.TrimSpace(string(out)) {
	case "true":
		return ScriptResult{Allow: true}
	case "false":
		return ScriptResult{Allow: false}
	}
	var obj struct {
		Allow  *bool  `json:"allow"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(out, &obj) == nil && obj.Allow != nil {
		return ScriptResult{Allow: *obj.Allow, Reason: obj.Reason}
	}
	return ScriptResult{Err: "the script must return true, false or {allow: boolean, reason?: string}"}
}

// actionOfItem rebuilds the Action of a stored item for rule checks.
func (s *Service) actionOfItem(ctx context.Context, req *entity.AccessRequest, it entity.AccessRequestItem) Action {
	var params map[string]any
	_ = json.Unmarshal([]byte(it.Params), &params)
	a := Action{SessionID: req.SessionID, AgentID: req.AgentID, AgentName: req.AgentName,
		ConnectorID: it.ConnectorID, ConnectorKey: it.ConnectorKey, OpKey: it.OpKey, Kind: it.Kind, Params: stringify(params)}
	if sc, ok := s.cat.(SubjectConfigs); ok {
		a.Config = sc.SubjectConfig(ctx, it.ConnectorID)
	}
	return a
}

// SubjectConfigs is the optional part of a Catalog that reads a
// connector's non-secret subject settings (its app_code).
type SubjectConfigs interface {
	SubjectConfig(ctx context.Context, connectorID string) map[string]string
}

// approverRule is one approvers rule matching an action.
type approverRule struct {
	rule entity.AccessRule
	doc  RuleDoc
}

// approverRules lists the active approvers rules that match the action.
func (s *Service) approverRules(ctx context.Context, a Action) []approverRule {
	if s.store == nil {
		return nil
	}
	rules, err := s.store.rules(ctx)
	if err != nil {
		return nil
	}
	sessionOwner, agentOwner := s.owners(ctx, a.SessionID, a.AgentID)
	var out []approverRule
	for _, r := range rules {
		if r.Effect != EffectApprovers || r.Disabled || r.Simulate || !s.ruleApplies(ctx, r, a.SessionID, sessionOwner, agentOwner) {
			continue
		}
		d, err := ParseRule(r.Doc)
		if err != nil || !s.Matches(ctx, d, a) {
			continue
		}
		out = append(out, approverRule{rule: r, doc: d})
	}
	return out
}

func viewerInput(p Person, ids map[string]string) map[string]any {
	v := map[string]any{"wick_id": p.ID, "name": p.Name, "is_admin": p.IsAdmin}
	for k, val := range ids {
		v[k] = val
	}
	return v
}

// ViewerIdentities is the optional part of a Directory that names a
// person's accounts on chat channels (e.g. {"email": …, "slack_id": …}),
// for approver scripts.
type ViewerIdentities interface {
	Identities(ctx context.Context, userID string) map[string]string
}

// itemRole decides whether viewer may approve one action: the default
// role (admin, session owner, agent owner) and the approvers rules that
// match it. "add" rules let in whoever their script allows; "only" rules
// must allow every approver. reason explains a refusal by a script.
func (s *Service) itemRole(ctx context.Context, a Action, viewer Person, defaultRole bool, rules []approverRule) (ok bool, reason string) {
	if len(rules) == 0 {
		return defaultRole, ""
	}
	var ids map[string]string
	if vi, has := s.dir.(ViewerIdentities); has {
		ids = vi.Identities(ctx, viewer.ID)
	}
	input := scriptInput(a, viewerInput(viewer, ids))
	added := false
	for _, ar := range rules {
		res := s.runRuleScript(ctx, ar.rule, ar.doc.When.Script, input)
		allow := res.Err == "" && res.Allow
		if ar.doc.Approvers == ApproversOnly {
			if !allow {
				why := res.Reason
				if res.Err != "" {
					why = "the check could not run"
				}
				if why == "" {
					why = "you do not pass it"
				}
				return false, "Rule \"" + ar.rule.Name + "\" decides who may approve this: " + why + "."
			}
			continue
		}
		if allow {
			added = true
		}
	}
	if defaultRole || added {
		return true, ""
	}
	return false, ""
}

// approverRuleNote names the add-rules for the "who can" line.
func approverRuleNote(rules []approverRule) string {
	var names []string
	for _, ar := range rules {
		if ar.doc.Approvers != ApproversOnly {
			names = append(names, "\""+ar.rule.Name+"\"")
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " Also anyone rule " + strings.Join(names, ", ") + " lets in."
}

// simulateScript runs one saved rule's script for a sample action and
// viewer, uncached, and reports what it returned.
func (s *Service) simulateScript(ctx context.Context, r *entity.AccessRule, a Action, viewerID string) (ScriptResult, error) {
	d, err := ParseRule(r.Doc)
	if err != nil {
		return ScriptResult{}, err
	}
	if strings.TrimSpace(d.When.Script) == "" {
		return ScriptResult{Err: "this rule has no script"}, nil
	}
	var viewer map[string]any
	if viewerID != "" {
		p, ok := s.person(ctx, viewerID)
		if !ok {
			p = Person{ID: viewerID}
		}
		var ids map[string]string
		if vi, has := s.dir.(ViewerIdentities); has {
			ids = vi.Identities(ctx, viewerID)
		}
		viewer = viewerInput(p, ids)
	}
	s.mu.RLock()
	run := s.script
	s.mu.RUnlock()
	if run == nil {
		return ScriptResult{Err: "rule scripts are not available on this wick"}, nil
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scriptTimeout)
	defer cancel()
	out, logs, rerr := run(sctx, r.OwnerID, d.When.Script, scriptInput(a, viewer))
	res := parseScriptResult(out, rerr)
	res.Logs = logs
	return res, nil
}
