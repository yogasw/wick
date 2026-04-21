package tags

import "github.com/yogasw/wick/pkg/tool"

// Default tag catalog. Add new shared tags here so every tool references
// the same spec — rename/flag changes happen in one place, and adding a
// tag to a tool is just appending `tags.Foo` to Meta().DefaultTags.
//
// Seeding rules (see tags.Service.EnsureToolDefaultTags):
//   - A tag with a given Name is created once. Existing tags keep their
//     flags — editing IsGroup/IsFilter here does NOT mutate an existing
//     row. Change the flags from /admin/tags instead.
//   - Links to a tool are written only on the first registration of
//     that tool (no tool_tag rows yet). Admin unlinks survive restarts.
var (
	// Text is the home-page group for text formatting / conversion /
	// manipulation tools.
	Text = tool.DefaultTag{
		Name:        "Text",
		Description: "Text formatting, conversion, and manipulation.",
		IsGroup:     true,
		SortOrder:   10,
	}

	// API groups developer-facing API tooling: request builders, mocking
	// servers, anything that helps poke at HTTP endpoints.
	API = tool.DefaultTag{
		Name:        "API",
		Description: "Build, mock, and inspect HTTP APIs.",
		IsGroup:     true,
		SortOrder:   30,
	}

	// Job groups background jobs that run on a cron schedule or are
	// triggered manually.
	Job = tool.DefaultTag{
		Name:        "Job",
		Description: "Background jobs with cron scheduling.",
		IsGroup:     true,
		SortOrder:   90,
	}
)
