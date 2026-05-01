package main

import (
	"github.com/yogasw/wick/app"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/job"
	"github.com/yogasw/wick/pkg/tool"

	"template/connectors/crudcrud"
	autogetdata "template/jobs/auto-get-data"
	"template/tags"
	converttext "template/tools/convert-text"
	"template/tools/external"
)

func main() {
	// One RegisterTool call = one card on the home grid. Call again with
	// a different meta.Key (and, if you want, a different Config) to get
	// a second card backed by the same Register func.
	app.RegisterTool(
		tool.Tool{
			Key:               "convert-text",
			Name:              "Convert Text",
			Description:       "Transform text between UPPERCASE, lowercase, Title Case, Sentence case, aLtErNaTiNg CaSe, or convert lines to/from literal \\n.",
			Icon:              "Aa",
			Category:          "Text",
			DefaultVisibility: entity.VisibilityPublic,
			DefaultTags:       []tool.DefaultTag{tags.Text},
		},
		converttext.Config{
			InitText: "hello world",
			InitType: "uppercase",
		},
		converttext.Register,
	)
	app.RegisterTool(
		tool.Tool{
			Key:               "convert-text-alt",
			Name:              "Convert Text (Alt)",
			Description:       "Second instance of convert-text — same logic, different card. Useful as a template for per-team or per-purpose duplicates.",
			Icon:              "aA",
			Category:          "Text",
			DefaultVisibility: entity.VisibilityPublic,
			DefaultTags:       []tool.DefaultTag{tags.Text},
		},
		converttext.Config{
			InitText: "HELLO WORLD",
			InitType: "lowercase",
		},
		converttext.Register,
	)

	// External links — each entry in external.All() becomes a card that
	// opens in a new tab. Edit template/tools/external/registry.go to
	// add, remove, or group your own links.
	for _, e := range external.All() {
		app.RegisterToolNoConfig(e.Meta, e.Register)
	}

	// One RegisterJob call = one row in the jobs table. Call again
	// with a different Key + Config to get a second scheduled instance
	// backed by the same Run func.
	app.RegisterJob(
		job.Meta{
			Key:         "auto-get-data",
			Name:        "Auto Get Data",
			Description: "Fetch a remote endpoint on a schedule.",
			Icon:        "🌐",
			DefaultCron: "*/30 * * * *",
			DefaultTags: []tool.DefaultTag{tags.Job},
		},
		autogetdata.Config{},
		autogetdata.Run,
	)
	app.Run()
}
