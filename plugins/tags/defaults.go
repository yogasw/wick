// Package tags is the shared default-tag catalog for plugins in this module.
// Reference these specs from a plugin's Meta.DefaultTags instead of hand-writing
// tag structs — it keeps the plugin in the same category section as wick's
// built-in connectors and avoids typo/drift across plugins.
//
//	import (
//	    "github.com/yogasw/wick/pkg/entity"
//	    "github.com/yogasw/wick/plugins/tags"
//	)
//
//	Meta.DefaultTags = []entity.DefaultTag{tags.Connector, tags.API}
//
// The Name values mirror wick's built-in catalog so a plugin lands under the
// matching section (API, Communication, …). Need a category that isn't here?
// Add it to this file (shared with every plugin), or declare it inline as a
// plain entity.DefaultTag — the app seeds any unknown tag on first registration.
package tags

import "github.com/yogasw/wick/pkg/entity"

var (
	// Connector groups LLM-facing connectors. Every connector plugin should
	// include this so it shows up under the "Connector" group.
	Connector = entity.DefaultTag{
		Name:        "Connector",
		Description: "LLM-callable connectors that wrap external APIs.",
		IsGroup:     true,
		SortOrder:   50,
	}

	// API groups developer-facing API tooling: request builders, mocking
	// servers, anything that pokes at HTTP endpoints.
	API = entity.DefaultTag{
		Name:        "API",
		Description: "Build, mock, and inspect HTTP APIs.",
		IsGroup:     true,
		SortOrder:   30,
	}

	// Communication groups chat / messaging connectors (Slack, Telegram,
	// Discord, email, …).
	Communication = entity.DefaultTag{
		Name:        "Communication",
		Description: "Chat and messaging connectors.",
		IsGroup:     true,
		SortOrder:   51,
	}

	// Development groups source-host and dev-platform connectors (GitHub,
	// Bitbucket, GitLab, Jira, CI, …).
	Development = entity.DefaultTag{
		Name:        "Development",
		Description: "Source hosts and developer platforms.",
		IsGroup:     true,
		SortOrder:   52,
	}

	// Observability groups logging / metrics / tracing connectors (Loki,
	// Grafana, Prometheus, Sentry, …).
	Observability = entity.DefaultTag{
		Name:        "Observability",
		Description: "Logging, metrics, and tracing connectors.",
		IsGroup:     true,
		SortOrder:   53,
	}

	// Browser groups connectors that drive a real browser (Playwright,
	// headless-Chrome scrapers, screenshot services, …).
	Browser = entity.DefaultTag{
		Name:        "Browser",
		Description: "Browser automation: screenshot, scrape, render, and scripted interaction.",
		IsGroup:     true,
		SortOrder:   54,
	}

	// Productivity groups docs / knowledge-base / note connectors (Notion,
	// Confluence, Google Docs, …).
	Productivity = entity.DefaultTag{
		Name:        "Productivity",
		Description: "Docs, wikis, and knowledge-base connectors.",
		IsGroup:     true,
		SortOrder:   55,
	}
)
