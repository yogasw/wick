package view

import (
	"context"
	"strings"
	"testing"
)

// TestConnectorsIndexPageRender renders the index with two category groups
// and asserts the page carries the search box, an "All" filter chip, a chip
// per group, the group headings, and links to each connector's rows page.
func TestConnectorsIndexPageRender(t *testing.T) {
	groups := []ConnectorIndexGroup{
		{Name: "Communication", Cards: []ConnectorIndexCard{
			{Key: "slack", Name: "Slack", Description: "Send messages", Icon: "💬", Category: "Communication", OpCount: 5, ActiveCount: 1},
		}},
		{Name: "Development", Cards: []ConnectorIndexCard{
			{Key: "github", Name: "GitHub", Icon: "🐙", Category: "Development", OpCount: 6, ActiveCount: 2, NeedsSetupCount: 3, DisabledCount: 1},
			{Key: "bitbucket", Name: "Bitbucket", Icon: "BB", Category: "Development", OpCount: 12, ActiveCount: 0},
		}},
	}

	var sb strings.Builder
	if err := ConnectorsIndexPage(groups, nil).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := sb.String()

	wantContains := []string{
		`id="search"`,                       // search box
		"Search connectors",                 // search placeholder
		`data-chip="all"`,                   // "All" filter chip
		`data-chip="Communication"`,         // category chip
		`data-chip="Development"`,           // category chip
		`data-group-name="Communication"`,   // grouped section
		`data-group-name="Development"`,     // grouped section
		`/manager/connectors/slack`,         // card link
		`/manager/connectors/github`,        // card link
		`/manager/connectors/bitbucket`,     // card link
		`connectors_index.js`,               // search/filter script
		"1 active",                          // active instance count
		"2 active",                          // active instance count
		"3 needs setup",                     // enabled-but-incomplete count
		"text-cau-400",                      // needs-setup count in amber
		"text-neg-400",                      // disabled count rendered in red
		"1 disabled",                        // disabled instance count
	}
	for _, want := range wantContains {
		if !strings.Contains(html, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}

	// Zero-count states must not render their note.
	for _, absent := range []string{"0 disabled", "0 needs setup"} {
		if strings.Contains(html, absent) {
			t.Errorf("rendered page should omit %q (zero-count state)", absent)
		}
	}
}
