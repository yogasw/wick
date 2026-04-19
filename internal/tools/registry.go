// Package tools is the central registry for every tool instance wick
// will mount. Downstream apps append to it via app.RegisterTool, and
// server.go walks All() at boot to validate, wire routes, and seed
// config rows.
//
// To add a built-in tool (wick lab binary only):
//  1. Create internal/tools/<name>/ with a Register(r tool.Router) func
//     and, if the tool has runtime-editable config, a Config struct.
//  2. Append one or more app.RegisterTool calls to RegisterBuiltins.
package tools

import (
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tags"
	pkgentity "github.com/yogasw/wick/pkg/entity"

	converttext "github.com/yogasw/wick/internal/tools/convert-text"
	"github.com/yogasw/wick/internal/tools/external"

	"github.com/yogasw/wick/pkg/tool"
)

// extra holds tool instances registered by downstream projects (and,
// for the wick lab binary, by RegisterBuiltins). All() returns this
// slice verbatim — wick's own in-house tools are opt-in.
var extra []tool.Module

// Register appends a fully-resolved Module record to the registry.
// Called from app.RegisterTool / app.RegisterToolNoConfig; do not call
// directly from app code.
func Register(m tool.Module) {
	extra = append(extra, m)
}

// RegisterBuiltins seeds wick's own in-house tools into the registry.
// Intended for the wick lab binary (cmd/lab) — downstream projects
// start with an empty registry and register only their own tools.
func RegisterBuiltins() {
	extra = append(extra, tool.Module{
		Meta: tool.Tool{
			Key:               "convert-text",
			Name:              "Convert Text",
			Description:       "Transform text between UPPERCASE, lowercase, Title Case, Sentence case, aLtErNaTiNg CaSe, or convert lines to/from literal \\n.",
			Icon:              "Aa",
			Category:          "Text",
			DefaultVisibility: entity.VisibilityPublic,
			DefaultTags:       []tool.DefaultTag{tags.Text},
		},
		Configs: pkgentity.StructToConfigs(converttext.Config{
			InitText: "hello world",
			InitType: "uppercase",
		}),
		Register: converttext.Register,
	})
	extra = append(extra, tool.Module{
		Meta: tool.Tool{
			Key:               "convert-text-alt",
			Name:              "Convert Text (Alt)",
			Description:       "Second instance of convert-text — same logic, different card. Useful as a template for per-team or per-purpose duplicates.",
			Icon:              "aA",
			Category:          "Text",
			DefaultVisibility: entity.VisibilityPublic,
			DefaultTags:       []tool.DefaultTag{tags.Text},
		},
		Configs: pkgentity.StructToConfigs(converttext.Config{
			InitText: "HELLO WORLD",
			InitType: "lowercase",
		}),
		Register: converttext.Register,
	})
	for _, e := range external.All() {
		extra = append(extra, e)
	}
}

// All returns every registered tool instance in registration order.
func All() []tool.Module {
	return extra
}
