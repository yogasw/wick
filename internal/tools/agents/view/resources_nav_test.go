package view

import (
	"context"
	"strings"
	"testing"
)

// render runs a templ component to a string.
func renderNav(t *testing.T, label string, active bool) string {
	t.Helper()
	var sb strings.Builder
	if err := agentsNavLink("/tools/agents/resources", label, active).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

// Every nav entry gets its icon from a switch on the LABEL. A label with
// no case renders a bare text link that looks broken next to its
// neighbours — which is exactly what shipped for this page.
func TestResourcesNavLinkHasIcon(t *testing.T) {
	got := renderNav(t, "Resources", false)

	if !strings.Contains(got, "<svg") {
		t.Fatalf("Resources nav link renders no icon:\n%s", got)
	}
	if !strings.Contains(got, "Resources") {
		t.Fatalf("Resources nav link lost its label:\n%s", got)
	}
}

// The entry lives inside the collapsed "More" group, and landing on the
// page must not leave the sidebar showing no sign of it. The group no
// longer auto-expands (nine rows pushed the projects list off screen);
// instead the active row is promoted above the summary — so what this
// guards now is that the page IS one of the group's rows and is the one
// marked active.
func TestResourcesIsPromotedOutOfTheMoreGroup(t *testing.T) {
	items := AgentsMoreItems(AgentsLayoutVM{Base: "/tools/agents", ActivePage: "resources"}, admin())

	for _, it := range items {
		if it.Label == "Resources" {
			if !it.Active {
				t.Fatal("the resources page does not mark its own nav row active, so nothing is promoted and the row stays hidden")
			}
			return
		}
	}
	t.Fatal("Resources is not in the More group at all")
}

// Guard the neighbours: every page in the group must map to a row, or
// landing there shows a sidebar with nothing highlighted.
func TestEveryMoreGroupPageHasARow(t *testing.T) {
	vm := AgentsLayoutVM{Base: "/tools/agents", ProvidersVisible: true, AirouterVisible: true}
	for _, page := range []string{
		"presets", "providers", "skills", "channels",
		"data-tables", "scheduled", "airouter", "agent-profiles", "resources",
	} {
		vm.ActivePage = page
		found := false
		for _, it := range AgentsMoreItems(vm, admin()) {
			if it.Active {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("page %q is in the More group but no row marks itself active for it", page)
		}
	}
}
