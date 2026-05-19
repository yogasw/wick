// Package mcp — diagnose: error-class classifier + suggested-fix
// builder. Surface point is the `diagnose=true` flag on
// workflow_get_run_log; this file is pure (RunState + Workflow + a few
// registries → Diagnosis) so a future workflow_watch diagnose-on-walk
// can re-use it without round-tripping back through the connector.
//
// Design rules:
//
//   - Classifier is a registry of regex rules, not a switch. Adding a
//     new error class = drop a new entry. Each rule is a self-contained
//     (pattern, handler) pair.
//
//   - Handlers run with a DiagnoseCtx that carries the run, the
//     workflow, and the registries they need to suggest a fix
//     (Integration for channel events/actions, Connectors for connector
//     ops, Providers for skills). When a registry is nil the handler
//     should degrade gracefully — diagnosis still surfaces the raw
//     error and "I don't know" instead of crashing.
//
//   - Per the no-auto-fix discipline: the result carries a
//     SuggestedFix description, never applies it. AI / user runs
//     workflow_update_node / workflow_write_file to act on it.
package mcp

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
	wftemplate "github.com/yogasw/wick/internal/agents/workflow/template"
)

// Diagnosis is the structured response attached to a failed-run reply
// when the caller passed `diagnose=true`. A successful run still gets a
// Diagnosis, but with ErrorClass empty and PathTaken populated.
type Diagnosis struct {
	ErrorClass    string         `json:"error_class,omitempty"`
	FailedNode    string         `json:"failed_node,omitempty"`
	Field         string         `json:"field,omitempty"`
	Summary       string         `json:"diagnosis,omitempty"`
	AvailableKeys []string       `json:"available_keys,omitempty"`
	SuggestedFix  *SuggestedFix  `json:"suggested_fix,omitempty"`
	PathTaken     []string       `json:"path_taken,omitempty"`
	NextActions   []string       `json:"next_actions,omitempty"`
	Status        string         `json:"status,omitempty"`
}

// SuggestedFix carries a concrete patch the AI can show to the user
// before running workflow_update_node. confidence is one of
// "high" | "medium" | "low" — see doc 25 §"Confidence levels".
type SuggestedFix struct {
	NodeID     string `json:"node_id"`
	Field      string `json:"field,omitempty"`
	Current    string `json:"current,omitempty"`
	Suggested  string `json:"suggested,omitempty"`
	Confidence string `json:"confidence"`
	Rationale  string `json:"rationale,omitempty"`
}

// DiagnoseCtx is the surface a classifier rule sees. Kept small and
// explicit so rules don't reach into mcp.Ops directly.
type DiagnoseCtx struct {
	Ctx         context.Context
	State       workflow.RunState
	Workflow    workflow.Workflow
	Integration *integration.Registry
	Connectors  *connector.Registry
	Providers   *provider.Registry
	// Match is the regex submatch slice (Match[0] is the full hit;
	// Match[1..] are capture groups). Always populated for rules that
	// supply a pattern.
	Match []string
}

// errorRule pairs a regex over the failed-node error message with a
// handler that turns the match into a Diagnosis. The first matching
// rule wins, in declaration order — keep the most specific patterns
// first.
type errorRule struct {
	class   string
	pattern *regexp.Regexp
	handler func(DiagnoseCtx) Diagnosis
}

// errorRules is the ordered classifier registry. Specific patterns
// must precede general ones; the loop returns on the first match.
var errorRules = []errorRule{
	{
		class:   "template_missing_key",
		pattern: regexp.MustCompile(`at <([^>]+)>: map has no entry for key "([^"]+)"`),
		handler: handleTemplateMissingKey,
	},
	{
		class:   "template_parse",
		pattern: regexp.MustCompile(`template parse:`),
		handler: handleTemplateParse,
	},
	{
		class:   "secret_leak_guard",
		pattern: regexp.MustCompile(`secret leak.*\.Env\.([A-Za-z_][A-Za-z0-9_]*)`),
		handler: handleSecretLeak,
	},
	{
		class:   "channel_action_missing",
		pattern: regexp.MustCompile(`channel action "([^"]+)" not registered`),
		handler: handleChannelActionMissing,
	},
	{
		class:   "connector_module_missing",
		pattern: regexp.MustCompile(`connector module "([^"]+)" not registered`),
		handler: handleConnectorModuleMissing,
	},
	{
		class:   "connector_op_missing",
		pattern: regexp.MustCompile(`(?:connector op|op) "([^"]+)" (?:on module "([^"]+)")?`),
		handler: handleConnectorOpMissing,
	},
	{
		class:   "provider_skill_missing",
		pattern: regexp.MustCompile(`(?:agent )?skill "([^"]+)" (?:not available|not found)`),
		handler: handleProviderSkillMissing,
	},
	{
		class:   "branch_no_edge_matched",
		pattern: regexp.MustCompile(`branch (\S+): no edge matched verdict "([^"]+)"`),
		handler: handleBranchNoEdge,
	},
	{
		class:   "agent_session_invalid",
		pattern: regexp.MustCompile(`session_from references nonexistent node "([^"]+)"`),
		handler: handleAgentSessionInvalid,
	},
}

// Diagnose returns the structured diagnosis for a run. Success paths
// produce a Diagnosis with Status="success" + PathTaken populated.
// Failed paths classify the error and (when possible) propose a fix.
//
// The function is safe to call with nil registries — handlers degrade
// to "I don't know" rather than crash.
func (m *Ops) Diagnose(ctx context.Context, w workflow.Workflow, st workflow.RunState) Diagnosis {
	if st.Status != "failed" || st.Error == nil {
		return Diagnosis{
			Status:    st.Status,
			PathTaken: append([]string{}, st.Completed...),
		}
	}
	dc := DiagnoseCtx{
		Ctx:         ctx,
		State:       st,
		Workflow:    w,
		Integration: m.Integration,
		Connectors:  m.Connectors,
		Providers:   m.providersRegistry(),
	}
	msg := st.Error.Message
	for _, rule := range errorRules {
		match := rule.pattern.FindStringSubmatch(msg)
		if match == nil {
			continue
		}
		dc.Match = match
		d := rule.handler(dc)
		if d.ErrorClass == "" {
			d.ErrorClass = rule.class
		}
		if d.FailedNode == "" {
			d.FailedNode = st.Error.Node
		}
		if d.PathTaken == nil {
			d.PathTaken = append([]string{}, st.Completed...)
		}
		if d.Status == "" {
			d.Status = st.Status
		}
		return d
	}
	// Unknown class — surface the raw error so AI sees something
	// concrete rather than a silent "unknown".
	return Diagnosis{
		ErrorClass: "unknown",
		FailedNode: st.Error.Node,
		Status:     st.Status,
		Summary:    msg,
		PathTaken:  append([]string{}, st.Completed...),
		NextActions: []string{
			"workflow_get_run_events to inspect the raw event stream",
			"file a wick issue with the error message if this should be classifiable",
		},
	}
}

// providersRegistry returns the provider registry for handlers that
// need it. Wrapper so future indirection (lazy load, swap) doesn't
// leak into every rule.
func (m *Ops) providersRegistry() *provider.Registry { return m.Providers }

// ── Rule handlers ──────────────────────────────────────────────────

// handleTemplateMissingKey: `at <.X.Y.Z>: map has no entry for key "K"`.
// Surface the parent path's available keys + closest match.
func handleTemplateMissingKey(dc DiagnoseCtx) Diagnosis {
	expr := dc.Match[1]
	missing := dc.Match[2]
	parent := expr
	if i := strings.LastIndex(expr, "."); i >= 0 {
		parent = expr[:i]
	}

	// Walk the parent path through the failed node's input context
	// (Event payload + already-completed node outputs) to find the
	// actual map and list its keys.
	avail := availableKeysForPath(dc, parent)
	sort.Strings(avail)

	d := Diagnosis{
		Field:         parent,
		AvailableKeys: avail,
		Summary: "Template references " + expr +
			" but the map at " + parent + " has no key \"" + missing + "\".",
	}

	if guess := bestMatch(missing, avail); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Field:      parent,
			Current:    "{{" + expr + "}}",
			Suggested:  "{{" + parent + "." + guess + "}}",
			Confidence: confidenceFor(missing, guess),
			Rationale:  "\"" + guess + "\" is the closest key present at " + parent + ".",
		}
	}
	d.NextActions = []string{
		"workflow_template_test(template=suggested, sample_event=<event>) to verify",
		"workflow_update_node(workflow_id, node_id, patch) once verified",
	}
	return d
}

// handleTemplateParse: bad template syntax. We can't always pinpoint
// the field, but the message + parse error give enough context.
func handleTemplateParse(dc DiagnoseCtx) Diagnosis {
	return Diagnosis{
		Summary: "Go template failed to parse. Check for unbalanced {{ }} or unsupported syntax in the node's templateable fields.",
		NextActions: []string{
			"workflow_node_detail(<type>) to find the node's templateable_fields",
			"workflow_template_test(template, sample_event=<event>) to isolate the bad snippet",
		},
	}
}

// handleSecretLeak: .Env.X tried to read a secret-tagged config.
// Propose the .Secret.X swap.
func handleSecretLeak(dc DiagnoseCtx) Diagnosis {
	name := dc.Match[1]
	return Diagnosis{
		Summary: ".Env." + name + " refers to a secret-tagged config. Use .Secret." + name + " to read it explicitly.",
		SuggestedFix: &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Current:    "{{.Env." + name + "}}",
			Suggested:  "{{.Secret." + name + "}}",
			Confidence: "high",
			Rationale:  "Wick blocks .Env reads of secret-tagged fields by design.",
		},
		NextActions: []string{
			"workflow_update_node to swap .Env.X → .Secret.X on the offending field",
		},
	}
}

// handleChannelActionMissing: action key "<ch>.<op>" not in integration
// registry. Suggest closest match scoped to the channel.
func handleChannelActionMissing(dc DiagnoseCtx) Diagnosis {
	key := dc.Match[1]
	d := Diagnosis{
		Summary: "Channel action \"" + key + "\" is not registered. The integration registry has no descriptor for this (channel, action) pair.",
	}
	if dc.Integration == nil {
		return d
	}
	channel, _, ok := splitDotted(key)
	if !ok {
		return d
	}
	candidates := []string{}
	for _, a := range dc.Integration.ActionsByChannel(channel) {
		candidates = append(candidates, a.Action)
	}
	if len(candidates) == 0 {
		d.Summary += " No actions are registered for channel \"" + channel + "\"."
		return d
	}
	_, op, _ := splitDotted(key)
	if guess := bestMatch(op, candidates); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Field:      "op",
			Current:    op,
			Suggested:  guess,
			Confidence: confidenceFor(op, guess),
			Rationale:  "\"" + guess + "\" is the closest registered action on channel \"" + channel + "\".",
		}
	}
	d.AvailableKeys = candidates
	return d
}

// handleConnectorModuleMissing: connector module name not registered.
func handleConnectorModuleMissing(dc DiagnoseCtx) Diagnosis {
	name := dc.Match[1]
	d := Diagnosis{
		Field:   "module",
		Summary: "Connector module \"" + name + "\" is not registered.",
	}
	if dc.Connectors == nil {
		return d
	}
	avail := dc.Connectors.List()
	d.AvailableKeys = avail
	if guess := bestMatch(name, avail); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Field:      "module",
			Current:    name,
			Suggested:  guess,
			Confidence: confidenceFor(name, guess),
			Rationale:  "\"" + guess + "\" is the closest registered connector module.",
		}
	}
	return d
}

// handleConnectorOpMissing: op X on module Y not found. Match
// capture[2] (module) optional.
func handleConnectorOpMissing(dc DiagnoseCtx) Diagnosis {
	op := dc.Match[1]
	module := ""
	if len(dc.Match) >= 3 {
		module = dc.Match[2]
	}
	d := Diagnosis{
		Field:   "op",
		Summary: "Connector op \"" + op + "\" not found",
	}
	if module != "" {
		d.Summary += " on module \"" + module + "\""
	}
	d.Summary += "."
	if dc.Connectors == nil || module == "" {
		return d
	}
	mod, ok := dc.Connectors.Module(module)
	if !ok {
		return d
	}
	avail := []string{}
	for _, o := range mod.Operations {
		avail = append(avail, o.Key)
	}
	d.AvailableKeys = avail
	if guess := bestMatch(op, avail); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Field:      "op",
			Current:    op,
			Suggested:  guess,
			Confidence: confidenceFor(op, guess),
			Rationale:  "\"" + guess + "\" is the closest registered op on \"" + module + "\".",
		}
	}
	return d
}

// handleProviderSkillMissing: agent skill X not available. Best-effort
// — we don't know which provider the failed node used, so list every
// provider's skills.
func handleProviderSkillMissing(dc DiagnoseCtx) Diagnosis {
	name := dc.Match[1]
	d := Diagnosis{
		Field:   "skills",
		Summary: "Agent skill \"" + name + "\" is not available on the configured provider.",
	}
	if dc.Providers == nil {
		return d
	}
	all := []string{}
	for _, p := range dc.Providers.List() {
		prov, _ := dc.Providers.Get(p)
		if prov == nil {
			continue
		}
		skills, err := prov.ListSkills(dc.Ctx)
		if err != nil {
			continue
		}
		for _, s := range skills {
			all = append(all, s.Name)
		}
	}
	d.AvailableKeys = uniqSorted(all)
	if guess := bestMatch(name, d.AvailableKeys); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Field:      "skills",
			Current:    name,
			Suggested:  guess,
			Confidence: confidenceFor(name, guess),
			Rationale:  "\"" + guess + "\" is the closest skill across registered providers.",
		}
	}
	return d
}

// handleBranchNoEdge: branch returned a verdict that no outgoing edge
// has a matching `case:` label. Surface the labels declared on the
// branch node's outgoing edges.
func handleBranchNoEdge(dc DiagnoseCtx) Diagnosis {
	nodeID := dc.Match[1]
	verdict := dc.Match[2]
	cases := []string{}
	for _, e := range dc.Workflow.Graph.Edges {
		if e.From == nodeID && e.Case != "" {
			cases = append(cases, e.Case)
		}
	}
	d := Diagnosis{
		Field:         "edges",
		AvailableKeys: cases,
		Summary: "Branch \"" + nodeID + "\" produced verdict \"" + verdict +
			"\" but no outgoing edge carries a matching case: label.",
	}
	if guess := bestMatch(verdict, cases); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     nodeID,
			Field:      "edge.case",
			Current:    verdict,
			Suggested:  guess,
			Confidence: confidenceFor(verdict, guess),
			Rationale:  "\"" + guess + "\" is the closest declared case on outgoing edges.",
		}
	} else {
		d.NextActions = []string{
			"add an edge with case: \"" + verdict + "\" or a default case: edge from \"" + nodeID + "\"",
		}
	}
	return d
}

// handleAgentSessionInvalid: session_from points at a node that
// doesn't exist or isn't an agent / session_init.
func handleAgentSessionInvalid(dc DiagnoseCtx) Diagnosis {
	ref := dc.Match[1]
	candidates := []string{}
	for _, n := range dc.Workflow.Graph.Nodes {
		if n.Type == workflow.NodeAgent || n.Type == "session_init" {
			candidates = append(candidates, n.ID)
		}
	}
	d := Diagnosis{
		Field:         "session_from",
		AvailableKeys: candidates,
		Summary: "session_from points at \"" + ref +
			"\" but no upstream agent / session_init node has that id.",
	}
	if guess := bestMatch(ref, candidates); guess != "" {
		d.SuggestedFix = &SuggestedFix{
			NodeID:     dc.State.Error.Node,
			Field:      "session_from",
			Current:    ref,
			Suggested:  guess,
			Confidence: confidenceFor(ref, guess),
			Rationale:  "\"" + guess + "\" is the closest agent/session_init node id.",
		}
	}
	return d
}

// ── Helpers ──────────────────────────────────────────────────────────

// availableKeysForPath looks up the node output map referenced by a
// `.Node.<id>.<...>` template path and returns the keys present at the
// final parent level. Falls back to scanning RunState.Outputs +
// RunState.Event when the path isn't a node reference.
func availableKeysForPath(dc DiagnoseCtx, path string) []string {
	parts := strings.Split(strings.TrimPrefix(path, "."), ".")
	if len(parts) < 2 {
		return nil
	}
	// Handle .Node.<id>.<rest>
	if parts[0] == "Node" {
		nodeID := parts[1]
		out, ok := dc.State.Outputs[nodeID]
		if !ok {
			// Try the trigger-as-node shorthand: when nodeID is the
			// trigger label, walk Event.Payload directly.
			if isTriggerLabel(nodeID, dc) {
				return drillKeys(map[string]any{"payload": payloadMap(dc.State.Event)}, parts[2:])
			}
			return nil
		}
		mp, ok := out.(map[string]any)
		if !ok {
			return nil
		}
		return drillKeys(mp, parts[2:])
	}
	if parts[0] == "Event" && len(parts) >= 2 && parts[1] == "Payload" {
		return drillKeys(payloadMap(dc.State.Event), parts[2:])
	}
	return nil
}

// drillKeys walks the remaining path through a generic map and returns
// the keys present at the leaf level. Stops on the first non-map
// segment.
func drillKeys(m map[string]any, rest []string) []string {
	cur := any(m)
	for _, p := range rest {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		nxt, ok := mp[p]
		if !ok {
			return nil
		}
		cur = nxt
	}
	mp, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(mp))
	for k := range mp {
		keys = append(keys, k)
	}
	return keys
}

func payloadMap(ev workflow.Event) map[string]any {
	if ev.Payload == nil {
		return map[string]any{}
	}
	return ev.Payload
}

// isTriggerLabel decides whether a node id in a template references a
// trigger node — best-effort, matches against Trigger.ID and the
// conventional "trigger" label.
func isTriggerLabel(name string, dc DiagnoseCtx) bool {
	if name == "trigger" {
		return true
	}
	for _, t := range dc.Workflow.Triggers {
		if t.ID == name {
			return true
		}
	}
	return false
}

// splitDotted splits "channel.action" / "module.op". Returns false
// when there's no dot at all.
func splitDotted(s string) (left, right string, ok bool) {
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// uniqSorted returns a sorted set of the input strings.
func uniqSorted(in []string) []string {
	seen := map[string]struct{}{}
	for _, s := range in {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// confidenceFor maps the Levenshtein distance between a typo and the
// suggested fix to a confidence level. Single-char swaps + substring
// matches → high; distance 2 → medium; everything else → low.
func confidenceFor(typo, guess string) string {
	if typo == "" || guess == "" {
		return "low"
	}
	if strings.EqualFold(typo, guess) {
		return "high"
	}
	d := levenshtein(strings.ToLower(typo), strings.ToLower(guess))
	switch {
	case d <= 1:
		return "high"
	case d == 2:
		return "medium"
	default:
		return "low"
	}
}

// Compile-time guard: ensure the template package is imported so its
// init runs even when no rule uses it directly. The classifier may
// grow rules that need wftemplate.Render later; keep the import live
// to avoid surprise dead-import lint errors.
var _ = wftemplate.Render
