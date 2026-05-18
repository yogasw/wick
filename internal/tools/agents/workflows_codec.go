package agents

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	wfnodes "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
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
	// Registry fallback: per-node module (internal/tools/agents/workflow/nodes/<type>/)
	// provides its own card render. New node types live here exclusively.
	if mod := wfnodes.ByType(t); mod != nil {
		r := mod.Render()
		return nodeRender{head: r.Head, hint: r.Hint, cssType: r.CSSType, inputs: r.Inputs, outputs: r.Outputs}
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
		data["session"] = n.Session
		data["session_from"] = n.SessionFrom
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
		if len(n.ArgModes) > 0 {
			data["__arg_modes"] = n.ArgModes
		}
	case wf.NodeConnector:
		data["module"] = n.Module
		data["op"] = n.Op
		if n.Args != nil {
			data["args"] = n.Args
		}
		if len(n.ArgModes) > 0 {
			data["__arg_modes"] = n.ArgModes
		}
	case wf.NodeBranch:
		data["expr"] = n.Expr
	case wf.NodeTransform:
		data["engine"] = n.Engine
		data["expression"] = n.Expression
	case wf.NodeEnd:
		data["result"] = n.Result
	default:
		// Registry fallback: per-node module owns its own field
		// projection. Loop preserves any keys the module hasn't
		// claimed so partial-fields still round-trip.
		if mod := wfnodes.ByType(n.Type); mod != nil {
			for k, v := range mod.DrawflowDataFromYAML(n) {
				data[k] = v
			}
		}
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
// workflow/mockup.html §3.
func workflowToDrawflowJSON(w wf.Workflow) (string, error) {
	positions := canvasPositions(w)
	nodes := map[string]drawflowNode{}
	nameToNumeric := map[string]int{}
	nextID := 1

	// One phantom node per trigger — each renders with its own
	// canvas ID (Trigger.ID) so the codec can identify it again on
	// the next save. The user can wire each phantom independently:
	// manual → http, slack → agent, cron → end. Engine.pickEntry
	// picks the right chain by matching evt.Type to Trigger.Type.
	for i, tr := range w.Triggers {
		id := triggerNodeID(tr, i)
		hint := triggerHint(tr)
		x, y := 40.0+float64(i)*220, 80.0
		if p, ok := positions[id]; ok {
			x, y = p[0], p[1]
		}
		// Lift every channel-trigger field the inspector reads back —
		// channel, event, target, plus the match form state (filter
		// toggle + spec + per-key Fixed/Expression modes). Without
		// this the form blanks on every reload.
		innerData := map[string]any{
			"triggerKind": string(tr.Type),
		}
		switch tr.Type {
		case wf.TriggerChannel:
			if tr.ChannelName != "" {
				innerData["channel"] = tr.ChannelName
			}
			if tr.Event != "" {
				innerData["event"] = tr.Event
			}
			if tr.Target != "" {
				innerData["target"] = tr.Target
			}
			if len(tr.Match) > 0 {
				innerData["match"] = tr.Match
			}
			if tr.MatchEnabled {
				innerData["match_enabled"] = true
			}
			if len(tr.MatchModes) > 0 {
				innerData["__match_modes"] = tr.MatchModes
			}
		case wf.TriggerCron:
			if tr.Schedule != "" {
				innerData["schedule"] = tr.Schedule
			}
			if tr.Timezone != "" {
				innerData["timezone"] = tr.Timezone
			}
		case wf.TriggerWebhook:
			if tr.Path != "" {
				innerData["path"] = tr.Path
			}
			if tr.Method != "" {
				innerData["method"] = tr.Method
			}
		case wf.TriggerManual:
			if tr.Label != "" {
				innerData["label"] = tr.Label
			}
		}
		nodes[strconv.Itoa(nextID)] = drawflowNode{
			ID:    nextID,
			Name:  id,
			Class: "node-trigger",
			HTML:  drawflowHTML("trigger", string(tr.Type), hint),
			Data: map[string]any{
				"id":   id,
				"type": "trigger",
				"data": innerData,
			},
			PosX:    x,
			PosY:    y,
			Inputs:  drawflowPorts(0, "input"),
			Outputs: drawflowPorts(1, "output"),
		}
		nameToNumeric[id] = nextID
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

	// Wire each trigger phantom to its declared EntryNode. Per-
	// trigger routing — manual → X, slack → Y, cron → Z — no flat
	// "_canvas.trigger_edges" list any more.
	for i, tr := range w.Triggers {
		if tr.EntryNode == "" {
			continue
		}
		trigID := triggerNodeID(tr, i)
		trigNum, ok := nameToNumeric[trigID]
		if !ok {
			continue
		}
		toNum, ok := nameToNumeric[tr.EntryNode]
		if !ok {
			continue
		}
		trigKey := strconv.Itoa(trigNum)
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
		parts := []string{}
		if tr.ChannelName != "" {
			parts = append(parts, tr.ChannelName)
		}
		if tr.Event != "" {
			parts = append(parts, tr.Event)
		}
		if tr.Target != "" {
			parts = append(parts, tr.Target)
		}
		if len(parts) == 0 {
			return ""
		}
		return strings.Join(parts, " · ")
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
//
// Deprecated: legacy reader for workflows saved before per-trigger
// routing. New saves emit one Trigger per canvas trigger node with
// the EntryNode set directly. Kept so a one-time migration can
// surface old fan-out data if needed.
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
func drawflowJSONToWorkflow(id, body string) (wf.Workflow, error) {
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
	// Collect every canvas trigger node + the node it fans out to.
	// Each trigger node owns its own routing — the engine uses
	// `Trigger.EntryNode` (matched by event type) to start the run,
	// so canvases can model "Slack fires chain A, cron fires chain
	// B" naturally.
	canvasTriggers := []canvasTrigger{}
	for _, i := range ids {
		dn := nodes[strconv.Itoa(i)]
		fromName := dn.Name
		isTriggerSrc := false
		if t, ok := dn.Data["type"].(string); ok && t == "trigger" {
			isTriggerSrc = true
		}
		if isTriggerSrc {
			ct := canvasTrigger{NodeID: dn.Name, Kind: triggerKindFromNode(dn)}
			// First outgoing connection becomes this trigger's entry.
			// Multiple outgoing edges aren't used yet — the canvas
			// allows them, the engine still runs one chain per fire.
			for _, port := range dn.Outputs {
				for _, c := range port.Connections {
					toIdx, _ := strconv.Atoi(c.Node)
					if toName, ok := numericToName[toIdx]; ok && ct.EntryNode == "" {
						ct.EntryNode = toName
					}
				}
			}
			canvasTriggers = append(canvasTriggers, ct)
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

	// Build the wf.Triggers list from the canvas trigger nodes. Each
	// canvas node becomes one Trigger entry; metadata (schedule,
	// channel name, …) is filled in later by the save handler from
	// the prev draft using Trigger.ID as the merge key.
	triggers := make([]wf.Trigger, 0, len(canvasTriggers))
	for _, ct := range canvasTriggers {
		tr := wf.Trigger{
			ID:        ct.NodeID,
			Type:      triggerTypeFromKind(ct.Kind),
			EntryNode: ct.EntryNode,
		}
		// Lift channel-specific metadata from the canvas node's data
		// block. Palette level-2 op rows seed channel + event into
		// `data.data` (e.g. drop "Slack → on_message" sets
		// channel="slack", event="message"). Without this lift the
		// codec emits a bare `type: channel` trigger that fails
		// validation with "channel is required".
		// Lift per-trigger-kind fields from the canvas node data block.
		// Each trigger kind reads its own subset; channel triggers
		// additionally carry the match filter form state.
		for _, dn := range nodes {
			if dn.Name != ct.NodeID {
				continue
			}
			inner, ok := dn.Data["data"].(map[string]any)
			if !ok {
				break
			}
			switch tr.Type {
			case wf.TriggerChannel:
				if v, ok := inner["channel"].(string); ok {
					tr.ChannelName = v
				}
				if v, ok := inner["event"].(string); ok {
					tr.Event = v
				}
				if v, ok := inner["target"].(string); ok {
					tr.Target = v
				}
				if v, ok := inner["match"].(map[string]any); ok && len(v) > 0 {
					tr.Match = v
					// Auto-sync Target from channel_id whitelist when not set explicitly.
					if tr.Target == "" {
						if ids, ok := v["channel_id"].([]any); ok && len(ids) > 0 {
							if first, ok := ids[0].(string); ok {
								tr.Target = first
							}
						}
					}
				}
				if v, ok := inner["match_enabled"].(bool); ok {
					tr.MatchEnabled = v
				}
				tr.MatchModes = stringMapFromAny(inner["__match_modes"])
			case wf.TriggerCron:
				if v, ok := inner["schedule"].(string); ok {
					tr.Schedule = v
				}
				if v, ok := inner["timezone"].(string); ok {
					tr.Timezone = v
				}
			case wf.TriggerWebhook:
				if v, ok := inner["path"].(string); ok {
					tr.Path = v
				}
				if v, ok := inner["method"].(string); ok {
					tr.Method = v
				}
			case wf.TriggerManual:
				if v, ok := inner["label"].(string); ok {
					tr.Label = v
				}
			}
			break
		}
		triggers = append(triggers, tr)
	}
	// Legacy fallback: workflows that predate per-trigger routing
	// expect a non-empty workflow.Triggers list. Keep emitting a
	// manual trigger when the canvas is empty so the editor doesn't
	// brick on first open. Triggers with no entry will still be
	// blocked by triggerHasEntry at Run Now time.
	if len(triggers) == 0 {
		triggers = []wf.Trigger{{Type: wf.TriggerManual, Label: "Run"}}
	}

	// graph.entry: pick the first trigger's EntryNode so legacy
	// engine paths (no Trigger.EntryNode match) still resolve.
	entry := ""
	for _, t := range triggers {
		if t.EntryNode != "" {
			entry = t.EntryNode
			break
		}
	}

	w := wf.Workflow{
		ID:       id,
		Version:  1,
		Name:     id,
		Enabled:  true,
		Triggers: triggers,
		Graph: wf.Graph{
			Entry: entry,
			Nodes: wfNodes,
			Edges: edges,
		},
		Canvas: map[string]any{
			"positions": positions,
		},
	}
	return w, nil
}

// canvasTrigger holds one trigger node's identity + wiring as seen
// on the canvas. Intermediate type used during decode before we
// build the YAML-shape wf.Trigger.
type canvasTrigger struct {
	NodeID    string
	Kind      string
	EntryNode string
}

// triggerNodeID returns the canvas node id used to render a Trigger
// as a phantom node. Prefers the persisted Trigger.ID (round-tripped
// from prior saves); falls back to `trigger-<type>-<idx>` so legacy
// YAML without IDs still gets stable, collision-free names when
// re-emitted to the canvas.
func triggerNodeID(tr wf.Trigger, idx int) string {
	if tr.ID != "" {
		return tr.ID
	}
	t := string(tr.Type)
	if t == "" {
		t = "manual"
	}
	if idx == 0 {
		return "trigger-" + t
	}
	return fmt.Sprintf("trigger-%s-%d", t, idx+1)
}

// triggerKindFromNode pulls the `triggerKind` field out of a
// Drawflow trigger node's data block. Defaults to "manual" — the
// canonical no-config trigger — so a freshly dragged trigger node
// still resolves to a meaningful wf.TriggerType.
func triggerKindFromNode(dn drawflowNode) string {
	inner, _ := dn.Data["data"].(map[string]any)
	if inner != nil {
		if k, ok := inner["triggerKind"].(string); ok && k != "" {
			return k
		}
	}
	return "manual"
}

// triggerTypeFromKind maps the canvas `triggerKind` to the engine's
// wf.TriggerType enum.
func triggerTypeFromKind(kind string) wf.TriggerType {
	switch kind {
	case "cron":
		return wf.TriggerCron
	case "channel":
		return wf.TriggerChannel
	case "webhook":
		return wf.TriggerWebhook
	case "error":
		return wf.TriggerError
	case "schedule_at":
		return wf.TriggerScheduleAt
	}
	return wf.TriggerManual
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
		wn.Session, _ = inner["session"].(string)
		wn.SessionFrom, _ = inner["session_from"].(string)
	case wf.NodeShell:
		wn.Command = stringSliceFromAny(inner["command"])
	case wf.NodeHTTP:
		wn.URL, _ = inner["url"].(string)
		wn.Method, _ = inner["method"].(string)
	case wf.NodeChannel:
		wn.ChannelName, _ = inner["channel"].(string)
		wn.Op, _ = inner["op"].(string)
		wn.Args, _ = inner["args"].(map[string]any)
		wn.ArgModes = stringMapFromAny(inner["__arg_modes"])
	case wf.NodeConnector:
		wn.Module, _ = inner["module"].(string)
		wn.Op, _ = inner["op"].(string)
		wn.Args, _ = inner["args"].(map[string]any)
		wn.ArgModes = stringMapFromAny(inner["__arg_modes"])
	case wf.NodeBranch:
		wn.Expr, _ = inner["expr"].(string)
	case wf.NodeTransform:
		wn.Engine, _ = inner["engine"].(string)
		wn.Expression, _ = inner["expression"].(string)
	case wf.NodeEnd:
		wn.Result, _ = inner["result"].(string)
	default:
		// Registry fallback. The module returns a wf.Node with the
		// type-specific fields populated; carry the ID over since the
		// switch path sets it before this point.
		if mod := wfnodes.ByType(wn.Type); mod != nil {
			fromMod := mod.YAMLFromDrawflowData(wn.ID, inner)
			fromMod.ID = wn.ID
			fromMod.Type = wn.Type
			wn = fromMod
		}
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

// stringMapFromAny coerces a canvas/data block value into the
// map[string]string shape wf.Node.ArgModes expects. Editors emit it
// as map[string]any (every JSON-derived map keys to any); YAML round
// trip from a hand-edited workflow can also yield the same. Returns
// nil when the source is empty so YAML omitempty kicks in.
func stringMapFromAny(v any) map[string]string {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		out := make(map[string]string, len(m))
		for k, x := range m {
			if s, ok := x.(string); ok && s != "" {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	if m, ok := v.(map[string]string); ok {
		if len(m) == 0 {
			return nil
		}
		return m
	}
	return nil
}
