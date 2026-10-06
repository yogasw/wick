package view

import (
	"context"
	"strings"
	"testing"
)

func renderProjectRow(t *testing.T, sharedBy string) string {
	t.Helper()
	var b strings.Builder
	if err := sidebarProjectRow("/b", "📁", "Slack Center", "p1", false, false, sharedBy).Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// A shared project shows only a hover icon beside the pin: no "Shared"
// pill eating the name's width, the owner on the tooltip and for screen
// readers.
func TestSidebarProjectRowSharedIsHoverIcon(t *testing.T) {
	html := renderProjectRow(t, "Rina")
	for _, want := range []string{
		`data-project-shared`,
		`title="Shared by Rina"`,
		`opacity-0 group-hover:opacity-100`,
		`<span class="sr-only">Shared by Rina</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("row missing %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, `>Shared</span>`) {
		t.Errorf("row still renders the text pill:\n%s", html)
	}
}

// A project the viewer owns carries no shared marker at all.
func TestSidebarProjectRowOwnedHasNoSharedMarker(t *testing.T) {
	if html := renderProjectRow(t, ""); strings.Contains(html, "data-project-shared") {
		t.Errorf("owned project rendered a shared marker:\n%s", html)
	}
}
