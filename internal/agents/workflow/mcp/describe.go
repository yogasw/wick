// Package mcp — workflow_describe implementation.
//
// One call returns a human-readable summary of a workflow: triggers,
// graph shape (entry/leaves/node count), declared dependencies
// (channels, connector modules, providers), plus a `issues` list of
// dangling edge targets and templates pointing at undeclared nodes.
//
// Designed as the "give me the lay of the land" call AI authors make
// before editing — replaces hand-walking workflow_get YAML.
package mcp

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
)

// DescribeResult is the workflow_describe response.
type DescribeResult struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Enabled      bool             `json:"enabled"`
	Summary      string           `json:"summary,omitempty"`
	Triggers     []TriggerSummary `json:"triggers"`
	Graph        GraphSummary     `json:"graph"`
	Dependencies DescribeDeps     `json:"dependencies"`
	Issues       []DescribeIssue  `json:"issues,omitempty"`
}

// TriggerSummary collapses a workflow.Trigger to the fields a human
// (or AI) needs at a glance.
type TriggerSummary struct {
	ID         string `json:"id,omitempty"`
	Type       string `json:"type"`
	EntryNode  string `json:"entry_node"`
	Schedule   string `json:"schedule,omitempty"`
	Channel    string `json:"channel,omitempty"`
	Event      string `json:"event,omitempty"`
	Path       string `json:"path,omitempty"`
	MatchOn    string `json:"match_on,omitempty"`
}

// GraphSummary describes the DAG's shape.
type GraphSummary struct {
	Entry     string   `json:"entry,omitempty"`
	NodeCount int      `json:"node_count"`
	EdgeCount int      `json:"edge_count"`
	Leaves    []string `json:"leaves,omitempty"`
	NodeTypes []string `json:"node_types,omitempty"`
}

// DescribeDeps lists external surfaces the workflow touches.
//
// Channels / Connectors / Providers stay as flat string lists so
// existing callers / UIs keep working. Other dependency kinds (sheets,
// webhooks, custom) surface under Other, keyed by Kind.
type DescribeDeps struct {
	Channels   []string            `json:"channels,omitempty"`
	Connectors []string            `json:"connectors,omitempty"`
	Providers  []string            `json:"providers,omitempty"`
	Other      map[string][]string `json:"other,omitempty"`
}

// DescribeIssue is one anomaly found during the walk. Level is
// "warning" or "error"; Message + Path tell the caller where to look.
type DescribeIssue struct {
	Level   string `json:"level"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// Describe builds the summary for one workflow id.
func (m *Ops) Describe(id string) (DescribeResult, error) {
	w, err := m.Service.Load(id)
	if err != nil {
		return DescribeResult{}, fmt.Errorf("load %s: %w", id, err)
	}
	out := DescribeResult{
		ID:      w.ID,
		Name:    w.Name,
		Enabled: w.Enabled,
		Summary: w.Description,
	}
	out.Triggers = summarizeTriggers(w.Triggers)
	out.Graph = summarizeGraph(w.Graph)
	out.Dependencies = m.collectDeps(w)
	out.Issues = m.findIssues(w)
	return out, nil
}

func summarizeTriggers(trs []workflow.Trigger) []TriggerSummary {
	out := make([]TriggerSummary, 0, len(trs))
	for _, t := range trs {
		ts := TriggerSummary{
			ID:        t.ID,
			Type:      string(t.Type),
			EntryNode: t.EntryNode,
			Schedule:  t.Schedule,
			Channel:   t.ChannelName,
			Event:     t.Event,
			Path:      t.Path,
		}
		if t.MatchEnabled && len(t.Match) > 0 {
			parts := []string{}
			for k := range t.Match {
				parts = append(parts, k)
			}
			sort.Strings(parts)
			ts.MatchOn = strings.Join(parts, ",")
		}
		out = append(out, ts)
	}
	return out
}

func summarizeGraph(g workflow.Graph) GraphSummary {
	out := GraphSummary{
		Entry:     g.Entry,
		NodeCount: len(g.Nodes),
		EdgeCount: len(g.Edges),
	}
	// Leaves = nodes that never appear as Edge.From.
	hasOut := map[string]bool{}
	for _, e := range g.Edges {
		hasOut[e.From] = true
	}
	typeSet := map[string]struct{}{}
	for _, n := range g.Nodes {
		typeSet[string(n.Type)] = struct{}{}
		if !hasOut[n.ID] {
			out.Leaves = append(out.Leaves, n.ID)
		}
	}
	sort.Strings(out.Leaves)
	for t := range typeSet {
		out.NodeTypes = append(out.NodeTypes, t)
	}
	sort.Strings(out.NodeTypes)
	return out
}

// collectDeps walks triggers + nodes and aggregates dependency
// declarations. Each node's executor MAY implement
// engine.DependencyDeclarer to expose what it touches; nodes without a
// declarer fall back to a generic switch over the well-known types
// (channel / connector / agent / classify). Anything past that is
// invisible to this layer — node authors are expected to declare via
// the declarer interface when they introduce a new external surface.
func (m *Ops) collectDeps(w workflow.Workflow) DescribeDeps {
	channels := map[string]struct{}{}
	connectors := map[string]struct{}{}
	providers := map[string]struct{}{}
	other := map[string]map[string]struct{}{}

	for _, t := range w.Triggers {
		if t.ChannelName != "" {
			channels[t.ChannelName] = struct{}{}
		}
	}

	collectInto := func(d engine.NodeDependency) {
		switch d.Kind {
		case engine.DepKindChannel:
			channels[d.Ref] = struct{}{}
		case engine.DepKindConnector:
			connectors[d.Ref] = struct{}{}
		case engine.DepKindProvider:
			providers[d.Ref] = struct{}{}
		default:
			if other[d.Kind] == nil {
				other[d.Kind] = map[string]struct{}{}
			}
			other[d.Kind][d.Ref] = struct{}{}
		}
	}

	for _, n := range w.Graph.Nodes {
		if exec := m.executorFor(n.Type); exec != nil {
			if dd, ok := exec.(engine.DependencyDeclarer); ok {
				for _, dep := range dd.Dependencies(n) {
					if dep.Ref == "" {
						continue
					}
					collectInto(dep)
				}
				continue
			}
		}
		// Generic fallback for nodes whose executor opts out of the
		// declarer interface. Covers the canonical built-ins so the
		// behaviour matches the pre-declarer code path.
		switch n.Type {
		case workflow.NodeChannel:
			if n.ChannelName != "" {
				channels[n.ChannelName] = struct{}{}
			}
		case workflow.NodeConnector:
			if n.Module != "" {
				ref := n.Module
				if n.Op != "" {
					ref += "." + n.Op
				}
				connectors[ref] = struct{}{}
			}
		case workflow.NodeAgent, workflow.NodeClassify:
			if n.Provider != "" {
				providers[n.Provider] = struct{}{}
			}
		}
	}

	deps := DescribeDeps{
		Channels:   sortedKeys(channels),
		Connectors: sortedKeys(connectors),
		Providers:  sortedKeys(providers),
	}
	if len(other) > 0 {
		deps.Other = map[string][]string{}
		for kind, set := range other {
			deps.Other[kind] = sortedKeys(set)
		}
	}
	return deps
}

// executorFor returns the engine-registered executor for a node type,
// or nil when none is registered. Engine may be nil during tests; in
// that case every node falls back to the generic switch above.
func (m *Ops) executorFor(t workflow.NodeType) workflow.Executor {
	if m.Engine == nil {
		return nil
	}
	return m.Engine.Executors[t]
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// templateNodeRefRx matches {{.Node.<id>.<rest>}} expressions so we
// can verify the referenced node id exists in the graph.
var templateNodeRefRx = regexp.MustCompile(`\.Node\.([A-Za-z_][A-Za-z0-9_]*)`)

// defaultTemplateableFields is the generic field pool wick scans on
// every node for {{.Node.X}} references. Covers the common shapes used
// by the built-in nodes (prompt, url, body, expr, input, expression,
// sql, plus prompt_file's referenced filename). Per-type executors
// extend this pool via TemplateableFieldsDeclarer when their schema
// adds fields outside this set.
func defaultTemplateableFields(n workflow.Node) map[string]string {
	return map[string]string{
		"prompt":      n.Prompt,
		"prompt_file": n.PromptFile,
		"url":         n.URL,
		"body":        n.Body,
		"expr":        n.Expr,
		"input":       n.Input,
		"expression":  n.Expression,
		"sql":         n.SQL,
	}
}

// findIssues looks for dangling edge targets, templates that point
// at undeclared nodes, and triggers without an entry_node.
func (m *Ops) findIssues(w workflow.Workflow) []DescribeIssue {
	out := []DescribeIssue{}
	nodeIDs := map[string]struct{}{}
	nodeLabels := map[string]struct{}{}
	for _, n := range w.Graph.Nodes {
		nodeIDs[n.ID] = struct{}{}
		if n.Label != "" {
			nodeLabels[n.Label] = struct{}{}
		}
	}
	// Triggers exposed as nodes too — `.Node.<trigger-id-or-label>` is
	// the canonical reference for trigger payload, so include them in
	// the lookup set.
	triggerRefs := map[string]struct{}{}
	for _, t := range w.Triggers {
		if t.ID != "" {
			triggerRefs[t.ID] = struct{}{}
		}
	}
	// "trigger" is the conventional default label workflows use in
	// examples — tolerate it as a valid reference even when the YAML
	// hasn't set an explicit Trigger.ID.
	triggerRefs["trigger"] = struct{}{}

	// Trigger entry_node existence.
	for i, t := range w.Triggers {
		if t.EntryNode == "" {
			out = append(out, DescribeIssue{
				Level:   "error",
				Path:    fmt.Sprintf("triggers[%d]", i),
				Message: "trigger has no entry_node",
			})
			continue
		}
		if _, ok := nodeIDs[t.EntryNode]; !ok {
			out = append(out, DescribeIssue{
				Level:   "error",
				Path:    fmt.Sprintf("triggers[%d].entry_node", i),
				Message: fmt.Sprintf("entry_node %q does not match any node id", t.EntryNode),
			})
		}
	}
	// Edge endpoints.
	for i, e := range w.Graph.Edges {
		if _, ok := nodeIDs[e.From]; !ok {
			out = append(out, DescribeIssue{
				Level:   "error",
				Path:    fmt.Sprintf("graph.edges[%d].from", i),
				Message: fmt.Sprintf("from %q does not match any node id", e.From),
			})
		}
		if _, ok := nodeIDs[e.To]; !ok {
			out = append(out, DescribeIssue{
				Level:   "error",
				Path:    fmt.Sprintf("graph.edges[%d].to", i),
				Message: fmt.Sprintf("to %q does not match any node id", e.To),
			})
		}
	}
	// Template node references — scan args / prompt / url / body / expr
	// for {{.Node.X}} and warn when X isn't a known node id or label.
	for _, n := range w.Graph.Nodes {
		fields := defaultTemplateableFields(n)
		// Executor opt-in: merge per-type declarer output so custom
		// node types (js / python / google_sheet …) can surface their
		// templateable fields without us hardcoding a switch here.
		if exec := m.executorFor(n.Type); exec != nil {
			if td, ok := exec.(engine.TemplateableFieldsDeclarer); ok {
				for k, v := range td.TemplateableFields(n) {
					fields[k] = v
				}
			}
		}
		for k, v := range fields {
			for _, ref := range templateNodeRefRx.FindAllStringSubmatch(v, -1) {
				name := ref[1]
				if _, ok := nodeIDs[name]; ok {
					continue
				}
				if _, ok := nodeLabels[name]; ok {
					continue
				}
				if _, ok := triggerRefs[name]; ok {
					continue
				}
				out = append(out, DescribeIssue{
					Level:   "warning",
					Path:    fmt.Sprintf("graph.nodes[%s].%s", n.ID, k),
					Message: fmt.Sprintf("template references {{.Node.%s.…}} but no node with id/label %q is declared", name, name),
				})
			}
		}
		// Also walk args map values.
		for argKey, raw := range n.Args {
			s, ok := raw.(string)
			if !ok {
				continue
			}
			for _, ref := range templateNodeRefRx.FindAllStringSubmatch(s, -1) {
				name := ref[1]
				if _, ok := nodeIDs[name]; ok {
					continue
				}
				if _, ok := nodeLabels[name]; ok {
					continue
				}
				if _, ok := triggerRefs[name]; ok {
					continue
				}
				out = append(out, DescribeIssue{
					Level:   "warning",
					Path:    fmt.Sprintf("graph.nodes[%s].args.%s", n.ID, argKey),
					Message: fmt.Sprintf("template references {{.Node.%s.…}} but no node with id/label %q is declared", name, name),
				})
			}
		}
	}
	return out
}
