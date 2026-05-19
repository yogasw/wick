// Package engine — optional declarer interfaces that let executors
// describe their per-node dependencies and templateable fields without
// the consumer (workflow_describe, validate, simulate) needing to
// hardcode a switch on NodeType.
//
// Both interfaces are OPTIONAL — executors that don't implement them
// keep working unchanged. workflow_describe falls back to a generic
// reflection-based scan that covers the common shapes (Prompt, URL,
// Body, Expr, Input, Expression, SQL, plus Args map values).
//
// Implement when:
//
//   - your node touches an external surface that wick should surface
//     in dependency listings (e.g. a `google_sheet` node should
//     declare the spreadsheet ID it reads as a dep), OR
//
//   - your node carries templateable strings on fields the generic
//     scan won't reach (custom field names, nested struct fields,
//     fields outside the wf.Node common pool).
//
// The wickdocs.Docs already covers documentation; declarers cover the
// runtime metadata callers need to walk a workflow without parsing
// YAML themselves.
package engine

import "github.com/yogasw/wick/internal/agents/workflow"

// NodeDependency is one external surface a node touches at runtime.
// Kind is a short tag the consumer uses to bucket entries; Ref is the
// human-readable identifier (channel name, connector module.op pair,
// provider name, sheet ID, …). Optional Details map carries
// kind-specific extras the consumer may want — keep it small.
type NodeDependency struct {
	Kind    string         `json:"kind"`
	Ref     string         `json:"ref"`
	Details map[string]any `json:"details,omitempty"`
}

// Canonical dependency kinds. Use these when applicable; emit a custom
// kind string only when none of these fit.
const (
	DepKindChannel   = "channel"
	DepKindConnector = "connector"
	DepKindProvider  = "provider"
	DepKindDataset   = "dataset"
	DepKindEnv       = "env"
	DepKindSecret    = "secret"
	DepKindWebhook   = "webhook"
	DepKindSheet     = "sheet"
	DepKindHTTP      = "http"
	DepKindFile      = "file"
)

// DependencyDeclarer is implemented by executors that want to surface
// their per-node dependencies through workflow_describe.
//
// Called once per Node when describing a workflow; the implementation
// inspects n.<fields> to decide what the node actually touches. Return
// nil for nodes whose runtime is pure / has no external dep.
type DependencyDeclarer interface {
	Dependencies(n workflow.Node) []NodeDependency
}

// TemplateableFieldsDeclarer is implemented by executors whose node
// type carries templateable strings on fields beyond the common pool
// scanned by default (Prompt, PromptFile, URL, Body, Expr, Input,
// Expression, SQL).
//
// Return name → value pairs for each templateable string field present
// on this node so the cross-ref scan in workflow_describe can find
// {{.Node.X}} references on custom fields. The `name` is the field
// label surfaced in the issue path (e.g. "code", "sheet_range") — pick
// labels that match the wick:"key=..." tag for the field when one
// exists.
type TemplateableFieldsDeclarer interface {
	TemplateableFields(n workflow.Node) map[string]string
}
