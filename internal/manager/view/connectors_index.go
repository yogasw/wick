package view

// ConnectorIndexCard is one connector definition rendered on the
// connectors index page (/manager/connectors). It collapses a module's
// Meta plus the number of rows the caller can manage into a single card
// that links to the per-connector rows page.
type ConnectorIndexCard struct {
	Key             string
	Name            string
	Description     string
	Icon            string
	Category        string // category tag name, used for grouping + filtering
	OpCount         int
	ActiveCount     int // enabled instances with complete config (ready to use)
	NeedsSetupCount int // enabled instances with required config still missing
	DisabledCount   int // instances the caller can manage that are row-disabled
	System          bool
	// Custom marks definitions that live in the custom_connectors table
	// instead of Go code; CustomSource carries the import origin shown on
	// the badge ("cURL" / "MCP" / "Manual"); NeedsReload flags a custom
	// def whose stored definition is newer than the module currently
	// serving.
	Custom       bool
	CustomSource string
	NeedsReload  bool
}

// ConnectorIndexGroup is a category section on the connectors index page,
// rendered as a bordered group card (same chrome as the home page groups).
// Name/Description come from the category tag; Cards are the connectors
// tagged with it, sorted by display name.
type ConnectorIndexGroup struct {
	Name        string
	Description string
	Cards       []ConnectorIndexCard
}
