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
