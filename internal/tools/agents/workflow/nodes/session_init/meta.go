// Package session_init is the editor-side module for the
// `session_init` workflow node — the canvas-visible declaration that
// sets rc.DefaultAgentSessionID for downstream agent nodes. Pairs
// with the engine-side executor in internal/agents/workflow/nodes.
//
// See internal/docs/workflow/pool.md for the run-time semantics; this
// file owns the UI surface only (palette entry, drawflow codec,
// inspector partial, JS module path).
package session_init

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeSessionInit }

func (m *module) PaletteSection() string { return "AI" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeSessionInit),
		Label: "session",
		Dot:   "bg-violet-300",
		Hint:  "default ID",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "session",
		Hint:    "default ID",
		CSSType: "session_init",
		Inputs:  1,
		Outputs: 1,
	}
}

// DrawflowDataFromYAML projects the persistent YAML fields into the
// inner `data.data` blob the inspector reads. Only the two
// session-specific fields are written; codec round-trip ignores the
// rest of wf.Node.
func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	return map[string]any{
		"preset":     n.Preset,
		"session_id": n.SessionID,
	}
}

// YAMLFromDrawflowData is the inverse — read the inspector's saved
// state back into a wf.Node. The inspector enforces the
// preset-vs-session_id mutual exclusion (one is empty), so we don't
// re-check here.
func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	preset, _ := inner["preset"].(string)
	sid, _ := inner["session_id"].(string)
	return wf.Node{
		ID:        id,
		Type:      wf.NodeSessionInit,
		Preset:    preset,
		SessionID: sid,
	}
}

// InspectorPartial returns the templ component for the parameters tab
// when a session_init node is selected. Layout convention: one
// <div class="wf-inspector-panel" data-node-type="session_init">
// block; editor.js shows/hides panels by matching data-node-type.
func (m *module) InspectorPartial() templ.Component { return Inspector() }

// InspectorScript is the URL path (under /static/nodes/) of the
// per-node JS module. Empty string would mean no behavioural hooks,
// but session_init needs UUID generation + mode toggle so we wire
// inspector.js.
func (m *module) InspectorScript() string { return "session_init/inspector.js" }
