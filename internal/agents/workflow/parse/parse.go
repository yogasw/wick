// Package parse decodes and validates workflow.yaml bodies. Pure
// in-memory transforms over the types defined in `workflow` root pkg
// — no filesystem, no engine, no executors.
//
// Use Parse to turn a byte slice into a Workflow + a synthesized ID,
// then Validate to gather every static problem (errors that block
// load + warnings that don't) before handing the workflow to engine
// or service layers.
package parse

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// IdentRe is the Go-template identifier pattern. Used for node ids,
// trigger ids, and labels — anything that will appear as a key in
// `{{.Node.<key>.…}}`. Letters/digits/underscore, must start with
// letter or underscore. Dashes are banned so the template parser
// doesn't choke with "bad character U+002D".
var IdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// IDRe is the canonical id pattern. Folder names and trigger
// path templates must match.
var IDRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// NodeIDRe accepts id charset plus underscore. Underscore is allowed
// because palette node-type names (e.g. `session_init`, `dataset_query`)
// are reused as the seeded ID on drop — rejecting `_` here would force
// every Go const to dual-spell as `session-init` solely for the
// validator. Folder names still use IDRe (hyphen-only).
var NodeIDRe = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Error is returned by Parse with a path-style locator for the
// offending field so callers (UI, MCP) can surface "yaml: graph.edges[2]: ...".
type Error struct {
	Path    string
	Message string
}

func (e Error) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidateID rejects names that would break path math.
func ValidateID(id string) error {
	if id == "" {
		return Error{Path: "id", Message: "is empty"}
	}
	if !IDRe.MatchString(id) {
		return Error{Path: "id", Message: fmt.Sprintf("%q is not [a-z0-9-]+", id)}
	}
	return nil
}

// ValidateNodeID rejects bad node IDs. Strict identifier rule — must
// work as a Go-template path segment in `{{.Node.<id>.…}}`.
func ValidateNodeID(id string) error {
	if id == "" {
		return Error{Path: "node.id", Message: "is empty"}
	}
	if !IdentRe.MatchString(id) {
		return Error{Path: "node.id", Message: fmt.Sprintf("%q must be a Go identifier ([A-Za-z_][A-Za-z0-9_]*) — no dash, dot, or space", id)}
	}
	return nil
}

// ValidateLabel rejects bad labels. Same rule as ValidateNodeID since
// labels are how operators reference nodes in templates.
func ValidateLabel(label string) error {
	if label == "" {
		return nil // optional field
	}
	if !IdentRe.MatchString(label) {
		return Error{Path: "node.label", Message: fmt.Sprintf("%q must be a Go identifier ([A-Za-z_][A-Za-z0-9_]*) — no dash, dot, or space", label)}
	}
	return nil
}

// Parse decodes a workflow.yaml body. The folder name is the
// authoritative ID — it overwrites whatever `id:` happens to be in the
// YAML so renaming a folder always wins over a stale value. The
// returned workflow has not yet been validated; call Validate after.
func Parse(id string, data []byte) (workflow.Workflow, error) {
	var w workflow.Workflow
	if err := yaml.Unmarshal(data, &w); err != nil {
		return workflow.Workflow{}, Error{Path: "yaml", Message: err.Error()}
	}
	w.ID = id
	if w.ID == "" {
		w.ID = uuid.NewString()
	}
	return w, nil
}

// Marshal serializes a Workflow back to YAML.
func Marshal(w workflow.Workflow) ([]byte, error) {
	return yaml.Marshal(w)
}

// Result is the aggregate of static checks performed by Validate.
// Ok() == true means no Errors (Warnings always allowed). Implements
// error so callers can `if err := r.AsError(); err != nil`.
type Result struct {
	Errors   []Error
	Warnings []Error
}

func (r *Result) Error() string {
	if r == nil || len(r.Errors) == 0 {
		return ""
	}
	if len(r.Errors) == 1 {
		return r.Errors[0].Error()
	}
	out := fmt.Sprintf("%d validation errors", len(r.Errors))
	for _, e := range r.Errors {
		out += "\n  - " + e.Error()
	}
	return out
}

// Ok reports whether validation found zero errors (warnings are fine).
func (r *Result) Ok() bool { return r == nil || len(r.Errors) == 0 }

// Validate checks the workflow body. Always returns a non-nil result;
// inspect Errors and Warnings.
func Validate(w workflow.Workflow) *Result {
	r := &Result{}

	if err := ValidateID(w.ID); err != nil {
		r.Errors = append(r.Errors, err.(Error))
	}
	if w.Name == "" {
		r.Errors = append(r.Errors, Error{Path: "name", Message: "is required"})
	}
	if len(w.Triggers) == 0 {
		r.Errors = append(r.Errors, Error{Path: "triggers", Message: "at least one trigger required"})
	}
	for i, tr := range w.Triggers {
		validateTrigger(r, fmt.Sprintf("triggers[%d]", i), tr)
	}
	if w.Graph.Entry == "" {
		anyEntry := false
		for _, tr := range w.Triggers {
			if tr.EntryNode != "" {
				anyEntry = true
				break
			}
		}
		if !anyEntry {
			r.Errors = append(r.Errors, Error{Path: "graph.entry", Message: "is required when no trigger sets entry_node"})
		}
	}
	if len(w.Graph.Nodes) == 0 {
		r.Errors = append(r.Errors, Error{Path: "graph.nodes", Message: "at least one node required"})
		return r
	}

	seen := map[string]int{}
	nodesByID := map[string]workflow.Node{}
	// labelOwner tracks which path first claimed each label (own id
	// counts when label is empty so id↔label collisions also surface).
	labelOwner := map[string]string{}
	for i, n := range w.Graph.Nodes {
		// Use the node ID in the path so the UI can index errors per
		// node element. Fall back to numeric index when the ID itself
		// is missing/invalid (otherwise the bracketed string would be
		// empty and the canvas badge has nothing to attach to).
		path := fmt.Sprintf("graph.nodes[%s]", n.ID)
		if n.ID == "" {
			path = fmt.Sprintf("graph.nodes[%d]", i)
		}
		if err := ValidateNodeID(n.ID); err != nil {
			r.Errors = append(r.Errors, Error{Path: path + ".id", Message: err.(Error).Message})
			continue
		}
		if err := ValidateLabel(n.Label); err != nil {
			r.Errors = append(r.Errors, Error{Path: path + ".label", Message: err.(Error).Message})
		}
		if prev, dup := seen[n.ID]; dup {
			r.Errors = append(r.Errors, Error{Path: path + ".id", Message: fmt.Sprintf("duplicate node ID %q (first at graph.nodes[%d])", n.ID, prev)})
			continue
		}
		key := n.Label
		if key == "" {
			key = n.ID
		}
		if owner, dup := labelOwner[key]; dup {
			r.Errors = append(r.Errors, Error{Path: path + ".label", Message: fmt.Sprintf("label/id %q collides with %s — labels must be unique within a workflow", key, owner)})
		} else {
			labelOwner[key] = path
		}
		seen[n.ID] = i
		nodesByID[n.ID] = n
		validateNodeBody(r, path, n)
	}

	if w.Graph.Entry != "" {
		if _, ok := nodesByID[w.Graph.Entry]; !ok {
			r.Errors = append(r.Errors, Error{Path: "graph.entry", Message: fmt.Sprintf("references unknown node %q", w.Graph.Entry)})
		}
	}
	for i, tr := range w.Triggers {
		if tr.EntryNode != "" {
			if _, ok := nodesByID[tr.EntryNode]; !ok {
				r.Errors = append(r.Errors, Error{Path: fmt.Sprintf("triggers[%d].entry_node", i), Message: fmt.Sprintf("references unknown node %q", tr.EntryNode)})
			}
		}
	}

	caseEdgesPerSource := map[string]map[string][]workflow.Edge{}
	incomingPerTarget := map[string][]workflow.Edge{}
	for i, e := range w.Graph.Edges {
		path := fmt.Sprintf("graph.edges[%d]", i)
		if e.From == "" || e.To == "" {
			r.Errors = append(r.Errors, Error{Path: path, Message: "from and to are required"})
			continue
		}
		from, fromOk := nodesByID[e.From]
		_, toOk := nodesByID[e.To]
		if !fromOk {
			r.Errors = append(r.Errors, Error{Path: path + ".from", Message: fmt.Sprintf("unknown node %q", e.From)})
		}
		if !toOk {
			r.Errors = append(r.Errors, Error{Path: path + ".to", Message: fmt.Sprintf("unknown node %q", e.To)})
		}
		if !fromOk || !toOk {
			continue
		}
		if e.Case != "" && !from.Type.IsBranchSource() {
			r.Errors = append(r.Errors, Error{Path: path + ".case", Message: fmt.Sprintf("case only valid on edge from classify/branch (from %q is %q)", e.From, from.Type)})
		}
		incomingPerTarget[e.To] = append(incomingPerTarget[e.To], e)
		if from.Type.IsBranchSource() {
			if caseEdgesPerSource[e.From] == nil {
				caseEdgesPerSource[e.From] = map[string][]workflow.Edge{}
			}
			caseEdgesPerSource[e.From][e.Case] = append(caseEdgesPerSource[e.From][e.Case], e)
		}
	}

	for _, n := range w.Graph.Nodes {
		if !n.Type.IsBranchSource() {
			continue
		}
		cases := caseEdgesPerSource[n.ID]
		if len(cases) == 0 {
			r.Errors = append(r.Errors, Error{Path: "graph.nodes[" + n.ID + "]", Message: "classify/branch has no outgoing edges"})
			continue
		}
		if _, hasDefault := cases["default"]; !hasDefault {
			r.Errors = append(r.Errors, Error{Path: "graph.edges", Message: fmt.Sprintf("classify/branch node %q missing default case edge", n.ID)})
		}
		if n.Type == workflow.NodeClassify {
			for _, oc := range n.OutputCases {
				if _, ok := cases[oc]; !ok {
					r.Warnings = append(r.Warnings, Error{Path: "graph.edges", Message: fmt.Sprintf("classify %q declares output_case %q with no matching edge", n.ID, oc)})
				}
			}
		}
	}

	// Fan-in warning for non-merge targets with all-parallel parents.
	for nid, edges := range incomingPerTarget {
		if len(edges) <= 1 {
			continue
		}
		n := nodesByID[nid]
		if n.Type == workflow.NodeMerge {
			continue
		}
		anyCaseFiltered := false
		for _, e := range edges {
			src := nodesByID[e.From]
			if src.Type.IsBranchSource() {
				anyCaseFiltered = true
				break
			}
		}
		if !anyCaseFiltered {
			r.Warnings = append(r.Warnings, Error{Path: "graph.edges", Message: fmt.Sprintf("node %q has %d parallel incoming edges; use merge node for wait-for-all semantics", nid, len(edges))})
		}
	}

	if cycle := DetectCycle(w.Graph); cycle != nil {
		r.Errors = append(r.Errors, Error{Path: "graph", Message: fmt.Sprintf("cycle detected involving nodes %v", cycle)})
	}

	roots := map[string]bool{}
	if w.Graph.Entry != "" {
		roots[w.Graph.Entry] = true
	}
	for _, tr := range w.Triggers {
		if tr.EntryNode != "" {
			roots[tr.EntryNode] = true
		}
	}
	reachable := BfsReachable(w.Graph, roots)
	for nid := range nodesByID {
		if !reachable[nid] {
			r.Warnings = append(r.Warnings, Error{Path: "graph.nodes", Message: fmt.Sprintf("node %q is unreachable from entry", nid)})
		}
	}

	return r
}

func validateTrigger(r *Result, path string, tr workflow.Trigger) {
	if tr.ID != "" && !IdentRe.MatchString(tr.ID) {
		r.Errors = append(r.Errors, Error{Path: path + ".id", Message: fmt.Sprintf("%q must be a Go identifier ([A-Za-z_][A-Za-z0-9_]*) — no dash, dot, or space", tr.ID)})
	}
	if err := ValidateLabel(tr.Label); err != nil {
		r.Errors = append(r.Errors, Error{Path: path + ".label", Message: err.(Error).Message})
	}
	switch tr.Type {
	case workflow.TriggerCron:
		if tr.Schedule == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".schedule", Message: "is required for cron trigger"})
		}
	case workflow.TriggerChannel:
		if tr.ChannelName == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".channel", Message: "is required for channel trigger"})
		}
		validateMatchSpec(r, path+".match", tr.Match)
	case workflow.TriggerWebhook:
		if tr.Path == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".path", Message: "is required for webhook trigger"})
		}
	case workflow.TriggerManual:
		// label optional
	case workflow.TriggerScheduleAt:
		if tr.At.IsZero() {
			r.Errors = append(r.Errors, Error{Path: path + ".at", Message: "is required for schedule_at trigger"})
		}
	case workflow.TriggerError:
		if tr.SourceWorkflow == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".source_workflow", Message: "is required for error trigger"})
		}
	case "":
		r.Errors = append(r.Errors, Error{Path: path + ".type", Message: "is required"})
	default:
		r.Errors = append(r.Errors, Error{Path: path + ".type", Message: fmt.Sprintf("unknown trigger type %q", tr.Type)})
	}
}

// validateMatchSpec warns when a match value looks like a plain string
// array (["C0ABC"]) — the router's idMembership checks .id inside each
// element, so plain strings never match. The correct picker format is
// a JSON array of {id,name} objects, e.g. [{"id":"C0ABC","name":"#ch"}].
// Plain string equality ("C0ABC") is also valid and is left as-is.
func validateMatchSpec(r *Result, path string, spec map[string]any) {
	for k, v := range spec {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "[") {
			continue
		}
		// Looks like a JSON array — check whether elements are plain
		// strings instead of {id,name} objects.
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(s), &arr); err != nil {
			r.Warnings = append(r.Warnings, Error{
				Path:    path + "." + k,
				Message: "value looks like JSON but failed to parse — use plain string equality or picker format [{\"id\":\"...\",\"name\":\"...\"}]",
			})
			continue
		}
		for i, elem := range arr {
			var obj map[string]any
			if json.Unmarshal(elem, &obj) != nil {
				// Element is not an object (likely a plain string like "C0ABC")
				r.Warnings = append(r.Warnings, Error{
					Path: fmt.Sprintf("%s.%s[%d]", path, k, i),
					Message: "picker array element must be an object {\"id\":\"...\",\"name\":\"...\"}, not a plain string — " +
						"plain string arrays never match; use [{\"id\":\"C0ABC\",\"name\":\"#channel\"}] or a bare string for single-value equality",
				})
				break
			}
			if _, hasID := obj["id"]; !hasID {
				r.Warnings = append(r.Warnings, Error{
					Path:    fmt.Sprintf("%s.%s[%d]", path, k, i),
					Message: "picker array element missing \"id\" field — router matches on id, not name",
				})
				break
			}
		}
	}
}

func validateNodeBody(r *Result, path string, n workflow.Node) {
	switch n.Type {
	case "":
		r.Errors = append(r.Errors, Error{Path: path + ".type", Message: "is required"})
	case workflow.NodeClassify:
		if n.Prompt == "" && n.PromptFile == "" {
			r.Errors = append(r.Errors, Error{Path: path, Message: "classify node needs prompt or prompt_file"})
		}
		if len(n.OutputCases) == 0 {
			r.Warnings = append(r.Warnings, Error{Path: path + ".output_cases", Message: "classify without output_cases will accept any verdict (defeats normalize/fuzzy)"})
		}
	case workflow.NodeAgent:
		if n.Prompt == "" && n.PromptFile == "" {
			r.Errors = append(r.Errors, Error{Path: path, Message: "agent node needs prompt or prompt_file"})
		}
	case workflow.NodeChannel:
		if n.ChannelName == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".channel", Message: "is required"})
		}
		if n.Op == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".op", Message: "is required"})
		}
	case workflow.NodeConnector:
		if n.Module == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".module", Message: "is required"})
		}
		if n.Op == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".op", Message: "is required"})
		}
	case workflow.NodeShell:
		if len(n.Command) == 0 {
			r.Errors = append(r.Errors, Error{Path: path + ".command", Message: "is required"})
		}
	case workflow.NodeHTTP:
		if n.URL == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".url", Message: "is required"})
		}
	case workflow.NodeDBQuery:
		if n.SQL == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".query", Message: "is required"})
		}
	case workflow.NodeTransform:
		if n.Engine == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".engine", Message: "is required"})
		}
		if n.Expression == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".expression", Message: "is required"})
		}
	case workflow.NodeBranch:
		if n.Expr == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".expr", Message: "is required"})
		}
	case workflow.NodeSwitch:
		if len(n.Cases) == 0 {
			r.Errors = append(r.Errors, Error{Path: path + ".cases", Message: "is required"})
		}
	case workflow.NodeGoScript:
		if n.Code == "" {
			r.Errors = append(r.Errors, Error{Path: path + ".code", Message: "is required"})
		}
	case workflow.NodeParallel:
		if len(n.Branches) == 0 {
			r.Errors = append(r.Errors, Error{Path: path + ".branches", Message: "is required"})
		}
	case workflow.NodeMerge:
		if len(n.Inputs) == 0 {
			r.Errors = append(r.Errors, Error{Path: path + ".inputs", Message: "is required"})
		}
	case workflow.NodeEnd, workflow.NodePython:
		// no required fields
	case workflow.NodeSessionInit:
		// session_init has no required fields: both `preset` and
		// `session_id` are optional — empty preset falls back to
		// workflow_run; empty session_id falls back to preset. Engine
		// resolves at runtime.
	default:
		if n.Type.IsDatasetNode() {
			if n.Dataset == "" {
				r.Errors = append(r.Errors, Error{Path: path + ".dataset", Message: "is required"})
			}
			return
		}
		r.Errors = append(r.Errors, Error{Path: path + ".type", Message: fmt.Sprintf("unknown node type %q", n.Type)})
	}
}

// hasCycle reports whether the graph contains at least one cycle using
// iterative DFS with a recursion-stack colour map (white/grey/black).
// This provides an independent cycle check complementary to DetectCycle.
func hasCycle(g workflow.Graph) bool {
	adj := map[string][]string{}
	for _, n := range g.Nodes {
		adj[n.ID] = nil // ensure every node is present even with no edges
	}
	for _, e := range g.Edges {
		if _, ok := adj[e.From]; ok {
			if _, ok := adj[e.To]; ok {
				adj[e.From] = append(adj[e.From], e.To)
			}
		}
	}
	// colour: 0 = white (unvisited), 1 = grey (in stack), 2 = black (done)
	colour := map[string]int{}
	type frame struct {
		node    string
		nbIndex int
	}
	for start := range adj {
		if colour[start] != 0 {
			continue
		}
		stack := []frame{{node: start, nbIndex: 0}}
		colour[start] = 1
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			neighbours := adj[top.node]
			if top.nbIndex < len(neighbours) {
				nb := neighbours[top.nbIndex]
				top.nbIndex++
				if colour[nb] == 1 {
					return true // back-edge → cycle
				}
				if colour[nb] == 0 {
					colour[nb] = 1
					stack = append(stack, frame{node: nb, nbIndex: 0})
				}
			} else {
				colour[top.node] = 2
				stack = stack[:len(stack)-1]
			}
		}
	}
	return false
}

// DetectCycle returns the IDs of nodes participating in a cycle, or
// nil if the graph is acyclic. Uses Kahn's topological sort.
func DetectCycle(g workflow.Graph) []string {
	inDeg := map[string]int{}
	adj := map[string][]string{}
	for _, n := range g.Nodes {
		inDeg[n.ID] = 0
	}
	for _, e := range g.Edges {
		if _, ok := inDeg[e.From]; !ok {
			continue
		}
		if _, ok := inDeg[e.To]; !ok {
			continue
		}
		inDeg[e.To]++
		adj[e.From] = append(adj[e.From], e.To)
	}
	queue := []string{}
	for id, d := range inDeg {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	removed := 0
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		removed++
		for _, nbr := range adj[head] {
			inDeg[nbr]--
			if inDeg[nbr] == 0 {
				queue = append(queue, nbr)
			}
		}
	}
	if removed == len(g.Nodes) {
		return nil
	}
	stuck := []string{}
	for id, d := range inDeg {
		if d > 0 {
			stuck = append(stuck, id)
		}
	}
	return stuck
}

// BfsReachable returns the set of node IDs reachable from any root.
func BfsReachable(g workflow.Graph, roots map[string]bool) map[string]bool {
	adj := map[string][]string{}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	reachable := map[string]bool{}
	queue := []string{}
	for r := range roots {
		queue = append(queue, r)
		reachable[r] = true
	}
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		for _, nbr := range adj[head] {
			if reachable[nbr] {
				continue
			}
			reachable[nbr] = true
			queue = append(queue, nbr)
		}
	}
	return reachable
}
