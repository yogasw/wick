// Package workflow holds the templ components for the Workflows tab
// under /tools/agents/workflows. Layout is split per section so each
// file maps 1:1 to a chunk of the mockup at
// internal/docs/workflow/mockup.html §3 (Canvas editor — live demo).
package workflow

import (
	wf "github.com/yogasw/wick/internal/agents/workflow"
	wfchannel "github.com/yogasw/wick/internal/agents/workflow/channel"
	wfconnector "github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/guard"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/tools/agents/view"
	wfnodes "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

// ListVM carries the workflow list page payload.
type ListVM struct {
	Layout    view.AgentsLayoutVM
	Base      string
	Workflows []mcp.Summary
}

// EditorVM carries the editor page payload — full workflow body
// serialized for both the Drawflow canvas and the YAML preview pane.
type EditorVM struct {
	Layout         view.AgentsLayoutVM
	Base           string
	ID             string
	Workflow       wf.Workflow
	HasDraft       bool
	YAML           string
	GraphJSON      string // serialized for Drawflow editor.import()
	ValidationJSON string // serialized validation report for initial paint
	Approved       bool
	GuardReport    *guard.Report
	NodeTypes      []mcp.NodeTypeInfo
	Palette        []PaletteSection // level-1 palette built from live registry
	Runs           []mcp.RunSummary
	RunsPage       int  // current 1-based page rendered in the Runs panel
	RunsHasMore    bool // older pages still exist
}

// RunVM carries the run-detail page payload.
type RunVM struct {
	Layout view.AgentsLayoutVM
	Base   string
	ID     string
	RunID  string
	State  wf.RunState
	Events []wf.RunEvent
}

// PaletteSection groups palette items per category for left sidebar.
type PaletteSection struct {
	Title string
	Items []PaletteItem
}

// PaletteItem is one row in the level-1 palette. Most items are a direct
// drag-source for a node type (e.g. http, classify). Items with Subitems
// populated are drill-targets — clicking them swaps the palette to a
// level-2 view listing the per-integration operations (Slack's
// on_message / send_message, Linear's create_issue, …). This matches
// n8n's two-level palette where the user picks "Slack" then "Send
// message" — keeps the level-1 list short while still surfacing every
// integration's full op catalog.
type PaletteItem struct {
	Type     string      // node type id (classify, agent, shell, ...)
	Label    string      // display label
	Dot      string      // tailwind bg-* color class for the leading dot
	Hint     string      // optional right-aligned hint
	Group    string      // drill-key (e.g. "channel-slack") — set when Subitems != nil
	Subitems []PaletteOp // level-2 operations
}

// PaletteOp is one operation under a drillable PaletteItem. The combo
// (NodeType + Defaults) is what gets attached to the new node when the
// user drags this row onto the canvas: NodeType selects the executor
// (channel / connector / trigger), Defaults pre-fills the data block
// (channel+op, module+op, channelName+event, …) so the inspector opens
// with the integration already wired.
type PaletteOp struct {
	NodeType string         // engine node kind: channel | connector | trigger | …
	Label    string         // display label (e.g. "Send message")
	Desc     string         // one-line hint shown under label
	Kind     string         // "trigger" | "action" — drives icon + grouping in L2
	Defaults map[string]any // node.data seed merged into the new node
}

// BuildPalette returns the level-1 palette with per-channel and
// per-connector drill-in rows woven in. Generic node types (cron,
// webhook, agent, http, …) stay as flat rows; integrations show up as
// drillable items.
//
// Sections:
//   - Triggers: cron / webhook / error / manual + per-channel triggers
//   - AI: classify / agent
//   - Action: shell / http / db_query / transform + per-channel actions + per-connector ops
//   - Logic: branch / parallel / end
func BuildPalette(channels []wfchannel.Info, connectors []wfconnector.Info) []PaletteSection {
	triggers := []PaletteItem{
		{Type: "trigger-cron", Label: "cron", Dot: "bg-indigo-500"},
		{Type: "trigger-webhook", Label: "webhook", Dot: "bg-indigo-500"},
		{Type: "trigger_manual", Label: "manual", Dot: "bg-indigo-500"},
		{Type: "trigger-error", Label: "error", Dot: "bg-red-500", Hint: "on fail"},
	}
	for _, ch := range channels {
		if len(ch.Triggers) == 0 {
			continue
		}
		ops := make([]PaletteOp, 0)
		for _, t := range ch.Triggers {
			for _, ev := range t.Events {
				ops = append(ops, PaletteOp{
					NodeType: "trigger",
					Label:    ev,
					Desc:     t.Description,
					Kind:     "trigger",
					Defaults: map[string]any{
						"triggerKind": "channel",
						"channel":     ch.Name,
						"event":       ev,
					},
				})
			}
		}
		if len(ops) == 0 {
			continue
		}
		triggers = append(triggers, PaletteItem{
			Type:     "channel-trigger",
			Label:    ch.Name,
			Dot:      "bg-indigo-500",
			Hint:     "trigger",
			Group:    "trigger-channel-" + ch.Name,
			Subitems: ops,
		})
	}

	actions := []PaletteItem{
		{Type: "shell", Label: "shell", Dot: "bg-slate-500"},
		{Type: "db_query", Label: "db_query", Dot: "bg-sky-500"},
		{Type: "transform", Label: "transform", Dot: "bg-cyan-500"},
	}
	for _, ch := range channels {
		if len(ch.Actions) == 0 {
			continue
		}
		ops := make([]PaletteOp, 0, len(ch.Actions))
		for _, a := range ch.Actions {
			ops = append(ops, PaletteOp{
				NodeType: "channel",
				Label:    a.ID,
				Desc:     a.Description,
				Kind:     "action",
				Defaults: map[string]any{
					"channel": ch.Name,
					"op":      a.ID,
				},
			})
		}
		actions = append(actions, PaletteItem{
			Type:     "channel-action",
			Label:    ch.Name,
			Dot:      "bg-indigo-500",
			Hint:     "channel",
			Group:    "channel-" + ch.Name,
			Subitems: ops,
		})
	}
	for _, m := range connectors {
		if len(m.Operations) == 0 {
			continue
		}
		ops := make([]PaletteOp, 0, len(m.Operations))
		for _, op := range m.Operations {
			ops = append(ops, PaletteOp{
				NodeType: "connector",
				Label:    op.Name,
				Desc:     op.Description,
				Kind:     "action",
				Defaults: map[string]any{
					"module": m.Module,
					"op":     op.Key,
				},
			})
		}
		actions = append(actions, PaletteItem{
			Type:     "connector",
			Label:    m.Name,
			Dot:      "bg-teal-500",
			Hint:     "connector",
			Group:    "connector-" + m.Module,
			Subitems: ops,
		})
	}

	sections := []PaletteSection{
		{Title: "Triggers", Items: triggers},
		{
			Title: "AI",
			Items: []PaletteItem{
				{Type: "classify", Label: "classify", Dot: "bg-pink-500"},
				{Type: "agent", Label: "agent", Dot: "bg-violet-500"},
			},
		},
		{Title: "Action", Items: actions},
		{
			Title: "Logic",
			Items: []PaletteItem{
				{Type: "branch", Label: "branch", Dot: "bg-red-500"},
				{Type: "parallel", Label: "parallel", Dot: "bg-lime-500"},
				{Type: "end", Label: "end", Dot: "bg-slate-700"},
			},
		},
	}
	// Append items contributed by per-node modules (see
	// internal/tools/agents/workflow/nodes/). Each module declares its
	// PaletteSection so the loop slots it into the existing section
	// list — adding a new node = drop a folder, no edit here.
	for _, m := range wfnodes.All() {
		item := PaletteItem{
			Type:  m.PaletteItem().Type,
			Label: m.PaletteItem().Label,
			Dot:   m.PaletteItem().Dot,
			Hint:  m.PaletteItem().Hint,
			Group: m.PaletteItem().Group,
		}
		matched := false
		for i := range sections {
			if sections[i].Title == m.PaletteSection() {
				sections[i].Items = append(sections[i].Items, item)
				matched = true
				break
			}
		}
		if !matched {
			sections = append(sections, PaletteSection{Title: m.PaletteSection(), Items: []PaletteItem{item}})
		}
	}
	return sections
}
