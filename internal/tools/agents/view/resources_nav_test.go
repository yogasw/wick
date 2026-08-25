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

// The entry lives inside the collapsed "More" group, so landing on the
// page must expand it — otherwise the active row is hidden and the page
// looks unreachable from the sidebar it is listed in.
func TestResourcesExpandsMoreGroup(t *testing.T) {
	attrs := agentsMoreOpenAttr("resources")
	if _, ok := attrs["open"]; !ok {
		t.Fatal("the More group stays collapsed on the resources page, hiding its own nav entry")
	}
}

// Guard the neighbours: a typo'd case label would silently drop an icon
// from a page nobody is currently looking at.
func TestMoreGroupPagesAllExpand(t *testing.T) {
	for _, page := range []string{
		"presets", "providers", "skills", "channels",
		"data-tables", "scheduled", "airouter", "agent-profiles", "resources",
	} {
		if _, ok := agentsMoreOpenAttr(page)["open"]; !ok {
			t.Fatalf("page %q is in the More group but does not expand it", page)
		}
	}
}
