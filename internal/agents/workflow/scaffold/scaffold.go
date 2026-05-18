// Package scaffold provides workflow starter templates used by
// `workflow_create` MCP op + UI "New workflow" form. Each Scaffold
// produces a valid workflow that passes parse.Validate so the
// operator can iterate from a working baseline.
package scaffold

import (
	"time"

	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// Workflow returns a starter workflow for a template name. Supported
// templates: empty (default), support-triage, incident-response,
// daily-digest.
//
// `name` is the display title (Workflow.Name); when non-empty it
// overrides whatever default the template would have set. `id` is
// the stable folder identifier — UUID for canvas-created workflows,
// kebab for legacy hand-edited ones.
func Workflow(id, name, template string) workflow.Workflow {
	if id == "" {
		id = uuid.NewString()
	}
	now := time.Now().UTC()
	base := workflow.Workflow{
		ID:        id,
		Version:   1,
		Enabled:   false,
		CreatedAt: now,
	}
	switch template {
	case "support-triage":
		base.Name = "Support triage"
		base.Description = "Classify inbound support messages and route to the right handler."
		base.Triggers = []workflow.Trigger{{Type: workflow.TriggerChannel, ChannelName: "slack", Event: "message", Target: "#support", EntryNode: "classify"}}
		base.Graph = workflow.Graph{
			Entry: "classify",
			Nodes: []workflow.Node{
				{ID: "classify", Type: workflow.NodeClassify, Provider: "claude",
					Prompt:      "Classify this support message: {{.Event.Payload.text}}",
					OutputCases: []string{"bug", "question", "default"}},
				{ID: "handle-bug", Type: workflow.NodeEnd, Result: "Bug: {{.Event.Payload.text}}"},
				{ID: "handle-question", Type: workflow.NodeEnd, Result: "Question: {{.Event.Payload.text}}"},
				{ID: "handle-default", Type: workflow.NodeEnd, Result: "Other: {{.Event.Payload.text}}"},
			},
			Edges: []workflow.Edge{
				{From: "classify", Case: "bug", To: "handle-bug"},
				{From: "classify", Case: "question", To: "handle-question"},
				{From: "classify", Case: "default", To: "handle-default"},
			},
		}
	case "incident-response":
		base.Name = "Incident response"
		base.Description = "Webhook-triggered incident response with parallel data gathering."
		base.Triggers = []workflow.Trigger{{Type: workflow.TriggerWebhook, Path: "/hooks/alerts", EntryNode: "gather"}}
		base.Graph = workflow.Graph{
			Entry: "gather",
			Nodes: []workflow.Node{
				{ID: "gather", Type: workflow.NodeParallel, Branches: []string{"fetch-metrics", "fetch-logs"}},
				{ID: "fetch-metrics", Type: workflow.NodeShell, Command: []string{"echo", "metrics"}},
				{ID: "fetch-logs", Type: workflow.NodeShell, Command: []string{"echo", "logs"}},
				{ID: "merge", Type: workflow.NodeMerge, Inputs: []string{"fetch-metrics", "fetch-logs"}, Strategy: workflow.MergeObject},
				{ID: "summary", Type: workflow.NodeEnd, Result: "incident summary"},
			},
			Edges: []workflow.Edge{
				{From: "gather", To: "fetch-metrics"},
				{From: "gather", To: "fetch-logs"},
				{From: "fetch-metrics", To: "merge"},
				{From: "fetch-logs", To: "merge"},
				{From: "merge", To: "summary"},
			},
		}
	case "daily-digest":
		base.Name = "Daily digest"
		base.Description = "Cron-triggered daily summary."
		base.Triggers = []workflow.Trigger{{Type: workflow.TriggerCron, Schedule: "0 8 * * *", Timezone: "UTC", EntryNode: "fetch"}}
		base.Graph = workflow.Graph{
			Entry: "fetch",
			Nodes: []workflow.Node{
				{ID: "fetch", Type: workflow.NodeShell, Command: []string{"echo", "daily fetch"}},
				{ID: "publish", Type: workflow.NodeEnd, Result: "digest"},
			},
			Edges: []workflow.Edge{{From: "fetch", To: "publish"}},
		}
	default:
		base.Name = "Untitled workflow"
		base.Description = "Empty workflow scaffold. Add nodes via canvas or YAML."
		base.Triggers = []workflow.Trigger{{Type: workflow.TriggerManual, Label: "Run", EntryNode: "start"}}
		base.Graph = workflow.Graph{
			Entry: "start",
			Nodes: []workflow.Node{{ID: "start", Type: workflow.NodeEnd, Result: "ok"}},
			Edges: []workflow.Edge{},
		}
	}
	if name != "" {
		base.Name = name
	}
	return base
}
