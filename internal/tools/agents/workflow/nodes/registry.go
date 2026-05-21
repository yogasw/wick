// Package nodes is the editor-side registry for workflow node types.
// Each node lives in its own subpackage (e.g. session_init/) and
// registers a Module describing its palette entry, canvas codec, and
// inspector partial + JS bundle. The Workflows tab composes the
// palette, drawflow codec, and inspector modal by iterating the
// registry — adding a new node type means dropping a new subfolder,
// not editing five centralized files.
//
// Existing node types (classify, agent, shell, http, …) still live in
// the hand-written switch tables in tools/agents and view/workflow.
// The registry sits alongside as a fallback: when the switch has no
// case, ByType(t) consults the registry. New types use the registry
// path exclusively; the legacy switches stay until a migration pass
// converts them.
package nodes

import (
	"sync"

	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

// Module describes one node type for the editor UI.
//
// PaletteItem is what the user drags from the level-1 palette.
// Render is the visual card on the canvas (head label, css class,
// port counts). The DrawflowData* pair codec round-trips wf.Node ↔
// the per-node `data.data` blob the canvas saves. InspectorPartial
// is the templ partial rendered inside the modal's parameters tab
// (one <div class="wf-inspector-panel" data-node-type="..."> block).
// InspectorScript is the path under /static/nodes/<type>/ for the
// per-node JS module that wires hydrate/save/onDrop hooks; empty
// string = no JS module (palette + codec only).
type Module interface {
	NodeType() wf.NodeType
	PaletteSection() string
	PaletteItem() PaletteItem
	Render() NodeRender
	DrawflowDataFromYAML(n wf.Node) map[string]any
	YAMLFromDrawflowData(id string, inner map[string]any) wf.Node
	InspectorPartial() templ.Component
	InspectorScript() string
}

// PaletteItem mirrors view/workflow.PaletteItem (lifted here so this
// package doesn't import the view package, avoiding a cycle).
type PaletteItem struct {
	Type  string
	Label string
	Dot   string
	Hint  string
	Group string
	// Skip signals BuildPalette to ignore this item. Modules that
	// contribute a grouped entry via PaletteGrouper set this on all
	// sibling items so only one collapsed row appears in the palette.
	Skip bool
}

// PaletteOp is one operation inside a drillable palette group.
type PaletteOp struct {
	NodeType string
	Label    string
	Desc     string
	Kind     string
	Defaults map[string]any
}

// PaletteGrouper is an optional interface a Module may implement to
// expose a single drillable palette row (like connector / channel)
// instead of one flat row per node type. BuildPalette checks for this
// interface via type assertion.
type PaletteGrouper interface {
	PaletteGroup() PaletteGroupEntry
}

// PaletteGroupEntry is the drillable palette row returned by PaletteGrouper.
type PaletteGroupEntry struct {
	Section string
	Item    PaletteItem
	Ops     []PaletteOp
}

// NodeRender holds the visual config for a canvas node card. Mirrors
// the unexported nodeRender struct in workflows_codec.go so the codec
// can build a card from either the legacy switch or the registry.
type NodeRender struct {
	Head    string
	Hint    string
	CSSType string
	Inputs  int
	Outputs int
}

var (
	mu      sync.RWMutex
	modules []Module
)

// Register adds a module to the registry. Called from each node
// subpackage's init() — the parent (tools/agents) blank-imports the
// node aggregator so every module loads at process start.
func Register(m Module) {
	mu.Lock()
	defer mu.Unlock()
	modules = append(modules, m)
}

// All returns every registered module in registration order.
func All() []Module {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Module, len(modules))
	copy(out, modules)
	return out
}

// ByType looks up a module by its workflow node type. Returns nil
// when nothing is registered — caller falls back to the legacy
// switch table.
func ByType(t wf.NodeType) Module {
	mu.RLock()
	defer mu.RUnlock()
	for _, m := range modules {
		if m.NodeType() == t {
			return m
		}
	}
	return nil
}
