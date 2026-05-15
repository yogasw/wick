package agents

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// drawflowNode mirrors Drawflow's editor.export() per-node shape.
// Per Drawflow docs, inputs/outputs are keyed by "input_N"/"output_N"
// and each value is a {connections: []} object — NOT a bare array.
type drawflowNode struct {
	ID       int                       `json:"id"`
	Name     string                    `json:"name"`
	Class    string                    `json:"class"`
	HTML     string                    `json:"html"`
	Data     map[string]any            `json:"data"`
	TypeNode bool                      `json:"typenode"`
	PosX     float64                   `json:"pos_x"`
	PosY     float64                   `json:"pos_y"`
	Inputs   map[string]drawflowPort   `json:"inputs"`
	Outputs  map[string]drawflowPort   `json:"outputs"`
}

// drawflowPort wraps the connections array, matching Drawflow's shape.
type drawflowPort struct {
	Connections []drawflowConn `json:"connections"`
}

type drawflowConn struct {
	Node   string `json:"node"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
}

// nodeRender is the per-type display + port config shared by both
// initial render (server) and runtime addNodeOfType (client).
type nodeRender struct {
	head    string
	hint    string
	cssType string
	inputs  int
	outputs int
}

func renderFor(t wf.NodeType) nodeRender {
	switch t {
	case wf.NodeClassify:
		return nodeRender{head: "classify", hint: "AI route", cssType: "classify", inputs: 1, outputs: 3}
	case wf.NodeAgent:
		return nodeRender{head: "agent", hint: "reasoning", cssType: "agent", inputs: 1, outputs: 1}
	case wf.NodeChannel:
		return nodeRender{head: "channel", hint: "send_message", cssType: "channel", inputs: 1, outputs: 1}
	case wf.NodeConnector:
		return nodeRender{head: "connector", hint: "module · op", cssType: "connector", inputs: 1, outputs: 1}
	case wf.NodeShell:
		return nodeRender{head: "shell", hint: "cmd", cssType: "shell", inputs: 1, outputs: 1}
	case wf.NodeHTTP:
		return nodeRender{head: "http", hint: "GET / POST", cssType: "http", inputs: 1, outputs: 1}
	case wf.NodeDBQuery:
		return nodeRender{head: "db_query", hint: "sql", cssType: "db_query", inputs: 1, outputs: 1}
	case wf.NodeBranch:
		return nodeRender{head: "branch", hint: "expr", cssType: "branch", inputs: 1, outputs: 2}
	case wf.NodeParallel:
		return nodeRender{head: "parallel", hint: "fan-out", cssType: "parallel", inputs: 1, outputs: 3}
	case wf.NodeMerge:
		return nodeRender{head: "merge", hint: "wait-for-all", cssType: "merge", inputs: 3, outputs: 1}
	case wf.NodeEnd:
		return nodeRender{head: "end", hint: "terminator", cssType: "end", inputs: 1, outputs: 0}
	case wf.NodeTransform:
		return nodeRender{head: "transform", hint: "gotemplate", cssType: "transform", inputs: 1, outputs: 1}
	}
	return nodeRender{head: string(t), hint: "", cssType: "shell", inputs: 1, outputs: 1}
}

func drawflowHTML(head, title, hint string) string {
	return fmt.Sprintf(`<div class="node-head">%s</div><div class="node-body"><div class="title">%s</div><div class="meta">%s</div></div>`, head, title, hint)
}

func drawflowPorts(n int, prefix string) map[string]drawflowPort {
	out := map[string]drawflowPort{}
	for i := 1; i <= n; i++ {
		out[fmt.Sprintf("%s_%d", prefix, i)] = drawflowPort{Connections: []drawflowConn{}}
	}
	return out
}

// nodeDataFromWorkflow projects wf.Node fields into the Drawflow data
// payload. We keep the wick-side fields under data.data so the
// inspector can read + write them safely.
func nodeDataFromWorkflow(n wf.Node) map[string]any {
	data := map[string]any{}
	switch n.Type {
	case wf.NodeClassify:
		data["prompt"] = n.Prompt
		data["preset"] = n.Preset
		data["cases"] = n.OutputCases
	case wf.NodeAgent:
		data["prompt"] = n.Prompt
		data["preset"] = n.Preset
	case wf.NodeShell:
		data["command"] = n.Command
	case wf.NodeHTTP:
		data["url"] = n.URL
		data["method"] = n.Method
	case wf.NodeChannel:
		data["channel"] = n.ChannelName
		data["op"] = n.Op
		if n.Args != nil {
			data["args"] = n.Args
		}
	case wf.NodeConnector:
		data["module"] = n.Module
		data["op"] = n.Op
		if n.Args != nil {
			data["args"] = n.Args
		}
	case wf.NodeBranch:
		data["expr"] = n.Expr
	case wf.NodeTransform:
		data["engine"] = n.Engine
		data["expression"] = n.Expression
	case wf.NodeEnd:
		data["result"] = n.Result
	}
	return map[string]any{
		"id":   n.ID,
		"type": string(n.Type),
		"data": data,
	}
}

// workflowToDrawflowJSON builds the editor.import() shape from a
// stored workflow. Coordinates come from Workflow.Canvas.positions
// when present; otherwise auto-layout into a left→right grid. The
// first trigger is rendered as a phantom node before the entry so the
// canvas visualizes "where the workflow fires from" — matching
// workflow-mockup.html §3.
func workflowToDrawflowJSON(w wf.Workflow) (string, error) {
	positions := canvasPositions(w)
	nodes := map[string]drawflowNode{}
	nameToNumeric := map[string]int{}
	nextID := 1

	// Phantom trigger node — visual only, not part of w.Graph.Nodes.
	// Connects out to the entry node so users see the trigger source
	// in the editor without conflating triggers with graph nodes.
	if entry := pickGraphEntry(w); entry != "" && len(w.Triggers) > 0 {
		tr := w.Triggers[0]
		hint := triggerHint(tr)
		// Anchor trigger inside the canvas (positive x) so it isn't
		// clipped by Drawflow's container. Auto-layout shifts other
		// nodes right via the i*260 offset below.
		x, y := 40.0, 80.0
		if p, ok := positions["__trigger__"]; ok {
			x, y = p[0], p[1]
		}
		nodes[strconv.Itoa(nextID)] = drawflowNode{
			ID:    nextID,
			Name:  "__trigger__",
			Class: "node-trigger",
			HTML:  drawflowHTML("trigger", string(tr.Type), hint),
			Data: map[string]any{
				"id":   "__trigger__",
				"type": "trigger",
				"data": map[string]any{"kind": string(tr.Type)},
			},
			PosX:    x,
			PosY:    y,
			Inputs:  drawflowPorts(0, "input"),
			Outputs: drawflowPorts(1, "output"),
		}
		nameToNumeric["__trigger__"] = nextID
		nextID++
	}

	for i, n := range w.Graph.Nodes {
		nodeID := nextID + i
		nameToNumeric[n.ID] = nodeID
		x, y := positions[n.ID][0], positions[n.ID][1]
		if x == 0 && y == 0 {
			// Offset by +280 so the auto-laid-out chain starts to the
			// right of the phantom trigger card.
			x = float64(280 + (i%4)*260)
			y = float64(80 + (i/4)*180)
		}
		meta := renderFor(n.Type)
		nodes[strconv.Itoa(nodeID)] = drawflowNode{
			ID:      nodeID,
			Name:    n.ID,
			Class:   "node-" + meta.cssType,
			HTML:    drawflowHTML(meta.head, n.ID, meta.hint),
			Data:    nodeDataFromWorkflow(n),
			PosX:    x,
			PosY:    y,
			Inputs:  drawflowPorts(meta.inputs, "input"),
			Outputs: drawflowPorts(meta.outputs, "output"),
		}
	}

	// Wire trigger → target edges. Primary source is the canvas
	// metadata `_canvas.trigger_edges` (every fan-out the user drew);
	// falls back to a single entry-node edge for workflows that have
	// no canvas state yet (freshly scaffolded or hand-edited yaml).
	trigNum, hasTrig := nameToNumeric["__trigger__"]
	if hasTrig {
		targets := triggerTargetsFromCanvas(w)
		if len(targets) == 0 {
			if entry := pickGraphEntry(w); entry != "" {
				targets = []string{entry}
			}
		}
		trigKey := strconv.Itoa(trigNum)
		for _, name := range targets {
			toNum, ok := nameToNumeric[name]
			if !ok {
				continue
			}
			toKey := strconv.Itoa(toNum)
			t := nodes[trigKey]
			out := t.Outputs["output_1"]
			out.Connections = append(out.Connections, drawflowConn{Node: toKey, Output: "input_1"})
			t.Outputs["output_1"] = out
			nodes[trigKey] = t

			e := nodes[toKey]
			in := e.Inputs["input_1"]
			in.Connections = append(in.Connections, drawflowConn{Node: trigKey, Input: "output_1"})
			e.Inputs["input_1"] = in
			nodes[toKey] = e
		}
	}
	// Wire edges into the per-node inputs/outputs maps.
	for _, e := range w.Graph.Edges {
		from := nameToNumeric[e.From]
		to := nameToNumeric[e.To]
		if from == 0 || to == 0 {
			continue
		}
		fromKey := strconv.Itoa(from)
		toKey := strconv.Itoa(to)
		fromN := nodes[fromKey]
		toN := nodes[toKey]
		// Pick the first available output_N port — multiple cases
		// each get their own port (output_1, output_2, ...).
		outPort := pickNextEmptyPort(fromN.Outputs, "output_")
		fromP := fromN.Outputs[outPort]
		fromP.Connections = append(fromP.Connections, drawflowConn{Node: toKey, Output: "input_1"})
		fromN.Outputs[outPort] = fromP

		inP := toN.Inputs["input_1"]
		inP.Connections = append(inP.Connections, drawflowConn{Node: fromKey, Input: outPort})
		toN.Inputs["input_1"] = inP

		nodes[fromKey] = fromN
		nodes[toKey] = toN
	}
	// Drawflow expects every module key — Other_module stub silences
	// the `Object.keys(null)` import crash on otherwise-empty graphs.
	doc := map[string]any{
		"drawflow": map[string]any{
			"Home":         map[string]any{"data": nodes},
			"Other_module": map[string]any{"data": map[string]any{}},
		},
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func pickNextEmptyPort(ports map[string]drawflowPort, prefix string) string {
	keys := []string{}
	for k := range ports {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if len(ports[k].Connections) == 0 {
			return k
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return prefix + "1"
}

// pickGraphEntry returns the workflow's entry node (first trigger's
// override, falling back to graph.entry).
func pickGraphEntry(w wf.Workflow) string {
	for _, tr := range w.Triggers {
		if tr.EntryNode != "" {
			return tr.EntryNode
		}
	}
	return w.Graph.Entry
}

// triggerHint returns the right-aligned sub-label shown under the
// trigger node — channel name for channel triggers, schedule for cron,
// path for webhooks, etc.
func triggerHint(tr wf.Trigger) string {
	switch tr.Type {
	case wf.TriggerChannel:
		if tr.Target != "" {
			return tr.ChannelName + " · " + tr.Target
		}
		return tr.ChannelName
	case wf.TriggerCron:
		return tr.Schedule
	case wf.TriggerWebhook:
		return tr.Path
	case wf.TriggerManual:
		return "manual"
	case wf.TriggerScheduleAt:
		return tr.At.Format("2006-01-02 15:04")
	case wf.TriggerError:
		return "on fail"
	}
	return string(tr.Type)
}

// triggerTargetsFromCanvas pulls the list of node ids the trigger
// fans out to, captured during save under `_canvas.trigger_edges`.
// Returns nil when the workflow predates this metadata; callers fall
// back to the single Graph.Entry node.
func triggerTargetsFromCanvas(w wf.Workflow) []string {
	if w.Canvas == nil {
		return nil
	}
	// The slice may come back as []any (after yaml decode) or as the
	// concrete []map[string]any (right after save before round-trip
	// through yaml). Handle both shapes — without this, the canvas
	// lost its fan-out edges immediately after save even though they
	// were stored correctly.
	out := []string{}
	switch raw := w.Canvas["trigger_edges"].(type) {
	case []any:
		for _, v := range raw {
			if m, ok := v.(map[string]any); ok {
				if to, ok := m["to"].(string); ok && to != "" {
					out = append(out, to)
				}
			}
		}
	case []map[string]any:
		for _, m := range raw {
			if to, ok := m["to"].(string); ok && to != "" {
				out = append(out, to)
			}
		}
	}
	return out
}

func canvasPositions(w wf.Workflow) map[string][2]float64 {
	out := map[string][2]float64{}
	if w.Canvas == nil {
		return out
	}
	positions, _ := w.Canvas["positions"].(map[string]any)
	for k, v := range positions {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		// YAML decoder yields int/int64 for whole numbers and float64
		// for decimals; JSON yields float64 always. Accept both so
		// positions roundtrip cleanly regardless of source format.
		out[k] = [2]float64{numToFloat(m["x"]), numToFloat(m["y"])}
	}
	return out
}

func numToFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	}
	return 0
}

// drawflowJSONToWorkflow parses Drawflow's editor.export() body into
// a workflow.Workflow.
func drawflowJSONToWorkflow(slug, body string) (wf.Workflow, error) {
	var doc struct {
		Drawflow struct {
			Home struct {
				Data map[string]drawflowNode `json:"data"`
			} `json:"Home"`
		} `json:"drawflow"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return wf.Workflow{}, fmt.Errorf("drawflow json: %w", err)
	}
	nodes := doc.Drawflow.Home.Data

	ids := make([]int, 0, len(nodes))
	for k := range nodes {
		i, _ := strconv.Atoi(k)
		ids = append(ids, i)
	}
	sort.Ints(ids)

	numericToName := map[int]string{}
	positions := map[string]any{}
	wfNodes := []wf.Node{}
	for _, i := range ids {
		dn := nodes[strconv.Itoa(i)]
		numericToName[i] = dn.Name
		// Phantom trigger nodes are visual-only — skip them in the
		// wf-side graph. Trigger metadata lives in workflow.Triggers,
		// which the save handler carries forward from disk.
		if t, ok := dn.Data["type"].(string); ok && t == "trigger" {
			positions[dn.Name] = map[string]any{"x": dn.PosX, "y": dn.PosY}
			continue
		}
		wn := workflowNodeFromDrawflow(dn)
		wfNodes = append(wfNodes, wn)
		positions[wn.ID] = map[string]any{"x": dn.PosX, "y": dn.PosY}
	}

	edges := []wf.Edge{}
	// trigger fan-out edges: stored separately under `_canvas.trigger_edges`
	// so the visual graph survives round-trip. The engine doesn't use
	// them (it only routes from workflow.Triggers + Graph.Entry), but
	// the canvas codec re-renders them on load.
	triggerEdges := []map[string]any{}
	for _, i := range ids {
		dn := nodes[strconv.Itoa(i)]
		fromName := dn.Name
		// trigger nodes only emit; the engine has no `trigger` node type
		// in its graph. Their connections are captured as canvas
		// metadata so the visual round-trips on reload — without this
		// every output beyond the first one disappeared after save.
		isTriggerSrc := false
		if t, ok := dn.Data["type"].(string); ok && t == "trigger" {
			isTriggerSrc = true
		}
		if isTriggerSrc {
			for _, port := range dn.Outputs {
				for _, c := range port.Connections {
					toIdx, _ := strconv.Atoi(c.Node)
					if toName, ok := numericToName[toIdx]; ok {
						triggerEdges = append(triggerEdges, map[string]any{"to": toName})
					}
				}
			}
			continue
		}
		for portKey, port := range dn.Outputs {
			for _, c := range port.Connections {
				toIdx, _ := strconv.Atoi(c.Node)
				toName, ok := numericToName[toIdx]
				if !ok {
					continue
				}
				// Skip edges into trigger placeholders (shouldn't exist).
				toData := nodes[c.Node].Data
				if t, ok := toData["type"].(string); ok && t == "trigger" {
					continue
				}
				edges = append(edges, wf.Edge{From: fromName, To: toName, Case: caseFromOutput(portKey, dn, nodes)})
			}
		}
	}

	w := wf.Workflow{
		Slug:    slug,
		Version: 1,
		Name:    slug,
		Enabled: true,
		Triggers: []wf.Trigger{{Type: wf.TriggerManual, Label: "Run"}},
		Graph: wf.Graph{
			Entry: pickEntryNode(wfNodes, edges),
			Nodes: wfNodes,
			Edges: edges,
		},
		Canvas: map[string]any{
			"positions":     positions,
			"trigger_edges": triggerEdges,
		},
	}
	return w, nil
}

func workflowNodeFromDrawflow(dn drawflowNode) wf.Node {
	wn := wf.Node{ID: dn.Name}
	if t, ok := dn.Data["type"].(string); ok {
		wn.Type = wf.NodeType(t)
	}
	inner, _ := dn.Data["data"].(map[string]any)
	if inner == nil {
		return wn
	}
	switch wn.Type {
	case wf.NodeClassify:
		wn.Prompt, _ = inner["prompt"].(string)
		wn.Preset, _ = inner["preset"].(string)
		wn.OutputCases = stringSliceFromAny(inner["cases"])
	case wf.NodeAgent:
		wn.Prompt, _ = inner["prompt"].(string)
		wn.Preset, _ = inner["preset"].(string)
	case wf.NodeShell:
		wn.Command = stringSliceFromAny(inner["command"])
	case wf.NodeHTTP:
		wn.URL, _ = inner["url"].(string)
		wn.Method, _ = inner["method"].(string)
	case wf.NodeChannel:
		wn.ChannelName, _ = inner["channel"].(string)
		wn.Op, _ = inner["op"].(string)
		wn.Args, _ = inner["args"].(map[string]any)
	case wf.NodeConnector:
		wn.Module, _ = inner["module"].(string)
		wn.Op, _ = inner["op"].(string)
		wn.Args, _ = inner["args"].(map[string]any)
	case wf.NodeBranch:
		wn.Expr, _ = inner["expr"].(string)
	case wf.NodeTransform:
		wn.Engine, _ = inner["engine"].(string)
		wn.Expression, _ = inner["expression"].(string)
	case wf.NodeEnd:
		wn.Result, _ = inner["result"].(string)
	}
	return wn
}

// caseFromOutput maps a port slot (output_1, output_2, ...) back to a
// case label for classify/branch sources. The classify node's stored
// cases array is keyed by position.
func caseFromOutput(portKey string, src drawflowNode, _ map[string]drawflowNode) string {
	if !strings.HasPrefix(portKey, "output_") {
		return ""
	}
	t, _ := src.Data["type"].(string)
	if t != string(wf.NodeClassify) && t != string(wf.NodeBranch) {
		return ""
	}
	idxStr := strings.TrimPrefix(portKey, "output_")
	idx, _ := strconv.Atoi(idxStr)
	if idx < 1 {
		return ""
	}
	inner, _ := src.Data["data"].(map[string]any)
	if inner == nil {
		return ""
	}
	cases := stringSliceFromAny(inner["cases"])
	if idx-1 < len(cases) {
		return cases[idx-1]
	}
	return ""
}

func pickEntryNode(nodes []wf.Node, edges []wf.Edge) string {
	hasIn := map[string]bool{}
	for _, e := range edges {
		hasIn[e.To] = true
	}
	for _, n := range nodes {
		if !hasIn[n.ID] {
			return n.ID
		}
	}
	if len(nodes) > 0 {
		return nodes[0].ID
	}
	return ""
}

func stringSliceFromAny(v any) []string {
	if s, ok := v.([]any); ok {
		out := make([]string, 0, len(s))
		for _, x := range s {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	if s, ok := v.([]string); ok {
		return s
	}
	return nil
}
