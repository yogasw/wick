// Package workflow holds the templ components for the Workflows tab
// under /tools/agents/workflows. Layout is split per section so each
// file maps 1:1 to a chunk of the mockup at
// internal/docs/workflow-mockup.html §3 (Canvas editor — live demo).
package workflow

import (
	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/guard"
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/tools/agents/view"
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
	Layout      view.AgentsLayoutVM
	Base        string
	Slug        string
	Workflow    wf.Workflow
	HasDraft    bool
	YAML        string
	GraphJSON   string // serialized for Drawflow editor.import()
	ValidationJSON string // serialized validation report for initial paint
	Approved    bool
	GuardReport *guard.Report
	NodeTypes   []mcp.NodeTypeInfo
	Runs        []string
}

// RunVM carries the run-detail page payload.
type RunVM struct {
	Layout view.AgentsLayoutVM
	Base   string
	Slug   string
	RunID  string
	State  wf.RunState
	Events []wf.RunEvent
}

// PaletteSection groups palette items per category for left sidebar.
type PaletteSection struct {
	Title string
	Items []PaletteItem
}

// PaletteItem is one drag-source row.
type PaletteItem struct {
	Type  string // node type id (classify, agent, shell, ...)
	Label string // display label
	Dot   string // tailwind bg-* color class for the leading dot
	Hint  string // optional right-aligned hint
}

// DefaultPalette returns the palette catalog matching the mockup §3.
func DefaultPalette() []PaletteSection {
	return []PaletteSection{
		{
			Title: "Triggers",
			Items: []PaletteItem{
				{Type: "trigger-cron", Label: "cron", Dot: "bg-indigo-500"},
				{Type: "trigger-channel", Label: "channel", Dot: "bg-indigo-500"},
				{Type: "trigger-webhook", Label: "webhook", Dot: "bg-indigo-500"},
				{Type: "trigger-error", Label: "error", Dot: "bg-red-500", Hint: "on fail"},
			},
		},
		{
			Title: "AI",
			Items: []PaletteItem{
				{Type: "classify", Label: "classify", Dot: "bg-pink-500"},
				{Type: "agent", Label: "agent", Dot: "bg-violet-500"},
			},
		},
		{
			Title: "Action",
			Items: []PaletteItem{
				{Type: "channel", Label: "channel", Dot: "bg-indigo-500", Hint: "action"},
				{Type: "connector", Label: "connector", Dot: "bg-teal-500"},
				{Type: "shell", Label: "shell", Dot: "bg-slate-500"},
				{Type: "http", Label: "http", Dot: "bg-amber-500"},
				{Type: "db_query", Label: "db_query", Dot: "bg-sky-500"},
			},
		},
		{
			Title: "Logic",
			Items: []PaletteItem{
				{Type: "branch", Label: "branch", Dot: "bg-red-500"},
				{Type: "parallel", Label: "parallel", Dot: "bg-lime-500"},
				{Type: "end", Label: "end", Dot: "bg-slate-700"},
			},
		},
	}
}
