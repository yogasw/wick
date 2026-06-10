package workflow

import (
	"fmt"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/scaffold"
)

// normalizeTriggerEntryNodes fills EntryNode on any trigger where it is
// empty, falling back to Graph.Entry. Prevents the trigger→graph
// disconnect that occurs when AI creates a workflow without entry_node.
func normalizeTriggerEntryNodes(w *wf.Workflow) {
	if w.Graph.Entry == "" {
		return
	}
	for i := range w.Triggers {
		if w.Triggers[i].EntryNode == "" {
			w.Triggers[i].EntryNode = w.Graph.Entry
		}
	}
}

// topDownLayout is a thin alias around scaffold.ApplyTopDownLayout
// kept so handlers in this package don't need to import the scaffold
// package directly. New callers should reach for
// scaffold.ApplyTopDownLayout straight.
func topDownLayout(w wf.Workflow) wf.Workflow {
	return scaffold.ApplyTopDownLayout(w)
}

// triggerCanvasID returns the canvas node ID for trigger at index idx,
// matching the codec convention in workflows_codec.go:triggerNodeID.
func triggerCanvasID(t wf.Trigger, idx int) string {
	if t.ID != "" {
		return t.ID
	}
	typ := string(t.Type)
	if typ == "" {
		typ = "manual"
	}
	if idx == 0 {
		return "trigger-" + typ
	}
	return fmt.Sprintf("trigger-%s-%d", typ, idx+1)
}
