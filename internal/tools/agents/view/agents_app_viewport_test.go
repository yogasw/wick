package view

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The Team shell follows the visual viewport so the on-screen keyboard
// does not leave a gap under the composer; 100svh stays the fallback.
func TestAgentsApp_SizedToVisualViewport(t *testing.T) {
	var buf bytes.Buffer
	if err := AgentsApp(AgentsAppVM{Base: "/tools/agents", AssetURL: "/x.js"}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		"height: calc(var(--wick-vvh, 100svh) - var(--wick-chrome-top, 0px))",
		"window.visualViewport",
		`setProperty("--wick-vvh"`,
		"window.scrollTo(0, 0)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("AgentsApp output missing %q", want)
		}
	}
	if strings.Count(html, "safe-area-inset-bottom") > 0 {
		t.Error("shell must not add its own safe-area padding")
	}
}

// The account menu's Light/Dark switch reads the mode and the user's
// paired theme ids off the mount point.
func TestAgentsApp_CarriesThemeForAccountMenu(t *testing.T) {
	var buf bytes.Buffer
	vm := AgentsAppVM{Base: "/tools/agents", AssetURL: "/x.js", ThemeMode: "dark", ThemeLight: "github-light", ThemeDark: "dracula"}
	if err := AgentsApp(vm).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-theme-mode="dark"`, `data-theme-light="github-light"`, `data-theme-dark="dracula"`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("AgentsApp output missing %s", want)
		}
	}
}
