// Package datatable is the editor-side UI module for the seven
// datatable_* workflow nodes: get, exists, query, count, insert,
// upsert, delete.
//
// One Module per node type registers a palette entry, a drawflow
// codec (round-trips the wf.Node fields the executor reads — Table,
// Where, Conditions, Key, RowValues, OrderBy, Limit, Offset), and a
// shared inspector partial. The inspector renders only the controls
// each op needs (e.g. `key` for get, `conditions` for query) by
// keying off the selected node's type.
//
// Engine-side executor + descriptors live in
// internal/agents/workflow/nodes; see datatable.go there.
package datatable

import (
	"strings"

	"github.com/a-h/templ"
	"gopkg.in/yaml.v3"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

// yamlMarshal / yamlUnmarshal alias yaml.v3 so the codec helpers
// below stay self-documenting without yet another import line per
// call site.
var (
	yamlMarshal   = yaml.Marshal
	yamlUnmarshal = yaml.Unmarshal
)

// nodeMeta defines a single datatable_* module: palette label/hint,
// canvas card shape (input/output port count, branch verdicts), and
// the fields it serialises into the drawflow `data.data` blob.
type nodeMeta struct {
	t       wf.NodeType
	label   string // palette + canvas head text
	hint    string // palette right-side hint, also under the canvas head
	inputs  int    // canvas input ports (always 1 today)
	outputs int    // 1 = single edge, 2 = branch (true/false or found/not_found)
}

func init() {
	for _, m := range allMeta {
		registry.Register(&module{m: m})
	}
}

// allMeta lists every datatable_* node. Branching variants (exists,
// get) have outputs=2; the canvas renders them as two-port heads so
// the user wires both verdicts. Other variants are single-output.
var allMeta = []nodeMeta{
	{t: wf.NodeDataTableGet, label: "datatable get", hint: "load by id", inputs: 1, outputs: 2},
	{t: wf.NodeDataTableExists, label: "datatable exists", hint: "row match?", inputs: 1, outputs: 2},
	{t: wf.NodeDataTableQuery, label: "datatable query", hint: "multi-row search", inputs: 1, outputs: 1},
	{t: wf.NodeDataTableCount, label: "datatable count", hint: "count rows", inputs: 1, outputs: 1},
	{t: wf.NodeDataTableInsert, label: "datatable insert", hint: "new row", inputs: 1, outputs: 1},
	{t: wf.NodeDataTableUpsert, label: "datatable upsert", hint: "insert or update", inputs: 1, outputs: 1},
	{t: wf.NodeDataTableDelete, label: "datatable delete", hint: "drop rows", inputs: 1, outputs: 1},
}

type module struct{ m nodeMeta }

func (m *module) NodeType() wf.NodeType  { return m.m.t }
func (m *module) PaletteSection() string { return "Data" }

func (m *module) PaletteItem() registry.PaletteItem {
	// All datatable ops are surfaced via PaletteGroup() on datatable_get.
	// Non-get modules return Skip so BuildPalette skips individual rows.
	if m.m.t != wf.NodeDataTableGet {
		return registry.PaletteItem{Skip: true}
	}
	return registry.PaletteItem{
		Type:  "datatable",
		Label: "datatable",
		Dot:   "bg-emerald-400",
		Hint:  "data tables",
		Group: "datatable",
	}
}

// PaletteGroup implements registry.PaletteGrouper for datatable_get,
// contributing one drillable "datatable" row listing all 7 ops.
func (m *module) PaletteGroup() registry.PaletteGroupEntry {
	ops := make([]registry.PaletteOp, 0, len(allMeta))
	for _, nm := range allMeta {
		ops = append(ops, registry.PaletteOp{
			NodeType: string(nm.t),
			Label:    nm.label,
			Desc:     nm.hint,
			Kind:     "action",
			Defaults: map[string]any{},
		})
	}
	return registry.PaletteGroupEntry{
		Section: "Data",
		Item: registry.PaletteItem{
			Type:  "datatable",
			Label: "datatable",
			Dot:   "bg-emerald-400",
			Hint:  "data tables",
			Group: "datatable",
		},
		Ops: ops,
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    m.m.label,
		Hint:    m.m.hint,
		CSSType: "datatable",
		Inputs:  m.m.inputs,
		Outputs: m.m.outputs,
	}
}

// DrawflowDataFromYAML emits the inner blob the inspector reads.
// Only the fields the executor consumes for this node type are
// included so the saved YAML stays minimal — e.g. `query` doesn't
// carry `key`, `insert` doesn't carry `where`.
func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	out := map[string]any{"table": n.Table}
	switch m.m.t {
	case wf.NodeDataTableGet:
		out["key"] = stringifyMap(n.Key)
	case wf.NodeDataTableExists, wf.NodeDataTableDelete, wf.NodeDataTableCount:
		out["where"] = stringifyMap(n.Where)
		if len(n.Conditions) > 0 {
			out["conditions"] = stringifyConditions(n.Conditions)
		}
		if len(n.ConditionModes) > 0 {
			out["__dt_modes"] = conditionModesToDTModes(n.ConditionModes)
		}
	case wf.NodeDataTableQuery:
		out["where"] = stringifyMap(n.Where)
		if len(n.Conditions) > 0 {
			out["conditions"] = stringifyConditions(n.Conditions)
		}
		if len(n.ConditionModes) > 0 {
			out["__dt_modes"] = conditionModesToDTModes(n.ConditionModes)
		}
		if len(n.OrderBy) > 0 {
			out["order_by"] = stringifyOrder(n.OrderBy)
		}
		if n.Limit > 0 {
			out["limit"] = n.Limit
		}
		if n.Offset > 0 {
			out["offset"] = n.Offset
		}
	case wf.NodeDataTableInsert, wf.NodeDataTableUpsert:
		out["row"] = stringifyMap(n.RowValues)
		if len(n.RowModes) > 0 {
			out["__dt_modes"] = rowModesToDTModes(n.RowModes)
		}
	}
	return out
}

// YAMLFromDrawflowData is the inverse — read what the inspector
// saved back into a wf.Node. Empty inspector fields stay zero-valued
// so the YAML doesn't carry stale keys after the user clears them.
func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: m.m.t}
	if v, ok := inner["table"].(string); ok {
		n.Table = strings.TrimSpace(v)
	}
	switch m.m.t {
	case wf.NodeDataTableGet:
		n.Key = parseInspectorMap(inner["key"])
	case wf.NodeDataTableExists, wf.NodeDataTableDelete, wf.NodeDataTableCount:
		n.Where = parseInspectorMap(inner["where"])
		n.Conditions = parseInspectorConditions(inner["conditions"])
		n.ConditionModes, _ = parseDTModes(inner["__dt_modes"])
	case wf.NodeDataTableQuery:
		n.Where = parseInspectorMap(inner["where"])
		n.Conditions = parseInspectorConditions(inner["conditions"])
		n.ConditionModes, _ = parseDTModes(inner["__dt_modes"])
		n.OrderBy = parseInspectorOrder(inner["order_by"])
		if v, ok := toInt(inner["limit"]); ok {
			n.Limit = v
		}
		if v, ok := toInt(inner["offset"]); ok {
			n.Offset = v
		}
	case wf.NodeDataTableInsert, wf.NodeDataTableUpsert:
		n.RowValues = parseInspectorMap(inner["row"])
		_, n.RowModes = parseDTModes(inner["__dt_modes"])
	}
	return n
}

func (m *module) InspectorPartial() templ.Component {
	// All 7 datatable types share one panel (data-node-type lists all).
	// Only the first entry emits HTML; the rest return nil so
	// nodeModulePartials() doesn't render 7 identical copies.
	if m.m.t == wf.NodeDataTableGet {
		return Inspector()
	}
	return nil
}
func (m *module) InspectorScript() string {
	if m.m.t == wf.NodeDataTableGet {
		return "datatable/inspector.js"
	}
	return ""
}

// ── codec helpers ───────────────────────────────────────────────────
//
// The inspector saves YAML literals (free-form textarea text) and the
// codec parses them into the strongly-typed wf.Node fields the
// executor reads. Round-trip back to text uses gopkg.in/yaml.v3 so
// blank input stays blank, and the rendered form is stable across
// save / load.

// stringifyMap renders a map[string]any as the original YAML body
// the user typed, falling back to yaml.Marshal so the round-trip is
// lossless even when the user pastes complex shapes (arrays, nested
// maps, dotted keys).
func stringifyMap(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, err := yamlMarshal(m)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// parseInspectorMap accepts whatever the inspector wrote (string body
// or already-decoded map) and returns a map suitable for the
// executor. Empty input → nil so the YAML stays clean.
func parseInspectorMap(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return nil
	case map[string]any:
		if len(x) == 0 {
			return nil
		}
		return x
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		var out map[string]any
		if err := yamlUnmarshal([]byte(s), &out); err != nil {
			// Treat unparseable input as empty rather than panicking
			// at save time; validation surfaces the error elsewhere.
			return nil
		}
		return out
	}
	return nil
}

// stringifyConditions emits the inspector textarea body for the
// condition list (yaml list of {column, op, value} objects).
func stringifyConditions(conds []wf.DataTableCondYAML) string {
	if len(conds) == 0 {
		return ""
	}
	b, err := yamlMarshal(conds)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// parseInspectorConditions accepts a yaml list (string body or
// already-decoded []any) and returns wf.DataTableCondYAML entries.
func parseInspectorConditions(v any) []wf.DataTableCondYAML {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		var out []wf.DataTableCondYAML
		if err := yamlUnmarshal([]byte(s), &out); err != nil {
			return nil
		}
		return out
	case []any:
		out := make([]wf.DataTableCondYAML, 0, len(x))
		for _, raw := range x {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, wf.DataTableCondYAML{
				Column: stringOf(m["column"]),
				Op:     stringOf(m["op"]),
				Value:  m["value"],
			})
		}
		return out
	}
	return nil
}

// stringifyOrder emits the inspector textarea body for the order-by
// list.
func stringifyOrder(order []wf.DataTableOrder) string {
	if len(order) == 0 {
		return ""
	}
	b, err := yamlMarshal(order)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}

// parseInspectorOrder reads the order-by textarea.
func parseInspectorOrder(v any) []wf.DataTableOrder {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		var out []wf.DataTableOrder
		if err := yamlUnmarshal([]byte(s), &out); err != nil {
			return nil
		}
		return out
	case []any:
		out := make([]wf.DataTableOrder, 0, len(x))
		for _, raw := range x {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, wf.DataTableOrder{
				Column:    stringOf(m["column"]),
				Direction: stringOf(m["direction"]),
			})
		}
		return out
	}
	return nil
}

func stringOf(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		// Drawflow saves numeric inputs as strings; parse loose so
		// "  42 " still hydrates.
		var n int
		for i, r := range s {
			if r >= '0' && r <= '9' {
				n = n*10 + int(r-'0')
				continue
			}
			if i == 0 && (r == '-' || r == '+') {
				continue
			}
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// conditionModesToDTModes converts ConditionModes (YAML key "c0","c1"...)
// to the __dt_modes map the JS inspector expects.
func conditionModesToDTModes(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// rowModesToDTModes converts RowModes (YAML key "r0","r1"...)
// to the __dt_modes map the JS inspector expects.
func rowModesToDTModes(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// parseDTModes reads __dt_modes from the inspector blob into separate
// ConditionModes (keys "c*") and RowModes (keys "r*") maps.
func parseDTModes(v any) (condModes, rowModes map[string]string) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil
	}
	for k, val := range m {
		s, _ := val.(string)
		if s == "" {
			continue
		}
		if len(k) > 0 && k[0] == 'c' {
			if condModes == nil {
				condModes = map[string]string{}
			}
			condModes[k] = s
		} else if len(k) > 0 && k[0] == 'r' {
			if rowModes == nil {
				rowModes = map[string]string{}
			}
			rowModes[k] = s
		}
	}
	return
}
