package agents

import "testing"

// Picker rows sort droppable (kind "drill") first, needs-connect
// placeholders (kind "drag", no payload) last, then alpha by label.
func TestSortEntryItems(t *testing.T) {
	items := []paletteItem{
		{Kind: "drag", Label: "Zeta · needs connect"},
		{Kind: "drill", Label: "Beta · @b", DrillKey: "connector-ops:gw:b:acc-b"},
		{Kind: "drill", Label: "Alpha · @a", DrillKey: "connector-ops:gw:a:acc-a"},
		{Kind: "drag", Label: "Alpha · needs connect"},
	}

	sortEntryItems(items)

	got := []string{items[0].Label, items[1].Label, items[2].Label, items[3].Label}
	want := []string{"Alpha · @a", "Beta · @b", "Alpha · needs connect", "Zeta · needs connect"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}
