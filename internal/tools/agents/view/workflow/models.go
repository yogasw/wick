// Package workflow holds the templ components for /tools/agents/workflows.
// Layout is split per section so each file maps to a chunk of the editor
// shell mounted around the Svelte SPA.
package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// ListVM carries the workflow list page payload.
type ListVM struct {
	Layout    view.AgentsLayoutVM
	Base      string
	Workflows []mcp.Summary
}
