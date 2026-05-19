// Package wickdocs defines the shared optional documentation struct
// embedded by every workflow descriptor (built-in node, channel event,
// channel action, connector operation, trigger type).
//
// Why this exists separately from the descriptors themselves: every
// descriptor wants the same optional "tell the AI what to actually do
// with this" fields — quirks, examples, templateable fields, pair-with
// links, common pitfalls — and we want the MCP layer to project them
// uniformly without per-source casting. Embedding one shared struct
// keeps the union of optional fields in one place and lets the MCP
// `workflow_node_detail` handler reach `.Docs` regardless of which
// registry it pulled the descriptor from.
//
// Lives in pkg/ rather than internal/agents/workflow/ so pkg/connector
// (which is the public connector contract) can embed it without
// breaking the "pkg never imports internal" rule. Workflow-side
// callers use it directly via this import path.
//
// All fields are opt-in. Zero-value Docs = current behaviour. The MCP
// layer skips empty fields when serialising so the AI never sees a
// `quirks: []` or `templateable_fields: null` that it would have to
// branch on.
package wickdocs

// Docs is the shared, opt-in documentation bundle. Embed by value in
// each descriptor — copying the struct is cheap (slices and maps are
// references) and avoids nil-pointer checks in the MCP projector.
//
// Field semantics:
//
//   - OutputShape: per-field 1-liner *beyond* the JSON Schema type.
//     Use it to explain how the field is used downstream (e.g.
//     "needed for update_modal"), not to repeat the type.
//   - TemplateableFields: nil = unknown / undocumented (no guarantee
//     about template rendering). Empty slice = no field is templated.
//     Non-empty = exactly those fields accept `{{...}}` Go templates.
//   - Quirks: one short imperative sentence per quirk — engine
//     normalisations, side effects, expiries, strict validation rules.
//   - Examples: hand-written, copy-pasteable. Auto-generation from the
//     schema is intentionally not supported (synthetic examples drift).
//   - PairWith: peer descriptor keys an AI should consider together,
//     e.g. `slack.open_modal` ↔ `slack.update_modal`. Keys follow the
//     same format the AI uses with `workflow_node_detail`.
//   - CommonPitfalls: short imperative mistakes to avoid. Distinct
//     from Quirks: pitfalls are AI-error-shaped ("don't do X first"),
//     quirks are behaviour-shaped ("X expires in 3s").
//   - InputSample / OutputSample: representative real request/response
//     JSON (string, not Go any) so MCP clients can render them as-is
//     and the editor UI can drop them into "try it" panels. Pick one
//     realistic-shape payload per descriptor — don't try to enumerate
//     every variant. Different from Examples, which are workflow YAML
//     usage patterns.
type Docs struct {
	OutputShape        map[string]string `json:"output_shape,omitempty"`
	TemplateableFields []string          `json:"templateable_fields,omitempty"`
	Quirks             []string          `json:"quirks,omitempty"`
	Examples           []Example         `json:"examples,omitempty"`
	PairWith           []string          `json:"pair_with,omitempty"`
	CommonPitfalls     []string          `json:"common_pitfalls,omitempty"`
	InputSample        string            `json:"input_sample,omitempty"`
	OutputSample       string            `json:"output_sample,omitempty"`
}

// Example is one named snippet. Name is a short slug ("basic",
// "with_structured_output", "skeleton_then_update") shown to the AI as
// section header; YAML is the copy-pasteable block, matching the live
// persistence format the engine reads.
type Example struct {
	Name string `json:"name"`
	YAML string `json:"yaml"`
}

// IsZero reports whether the bundle carries no information. The MCP
// projector uses this to omit the entire `docs:` section from the
// response when a descriptor opted out of the contract.
func (d Docs) IsZero() bool {
	return len(d.OutputShape) == 0 &&
		len(d.TemplateableFields) == 0 &&
		len(d.Quirks) == 0 &&
		len(d.Examples) == 0 &&
		len(d.PairWith) == 0 &&
		len(d.CommonPitfalls) == 0 &&
		d.InputSample == "" &&
		d.OutputSample == ""
}
