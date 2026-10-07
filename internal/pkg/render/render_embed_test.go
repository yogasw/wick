package render

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/pkg/tool"
)

const bodyMarker = "TOOL-BODY-HERE"

func renderPage(t *testing.T, meta tool.Tool, embedded bool) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, meta.Path, nil)
	r = r.WithContext(ui.WithEmbedded(r.Context(), embedded))
	w := httptest.NewRecorder()
	body := templ.ComponentFunc(func(_ context.Context, out io.Writer) error {
		_, err := io.WriteString(out, `<main class="px-6">`+bodyMarker+`</main>`)
		return err
	})
	NewToolRenderer(true)(tool.NewCtx(w, r, nil, meta, nil, nil), body)
	return w.Body.String()
}

var plainTool = tool.Tool{Key: "convert-text", Name: "Convert Text", Description: "Change text case", Icon: "Aa", Path: "/tools/convert-text"}

func TestToolPageKeepsChromeOutsideAFrame(t *testing.T) {
	out := renderPage(t, plainTool, false)
	if !strings.Contains(out, bodyMarker) {
		t.Fatal("tool body missing from the page")
	}
	if !strings.Contains(out, plainTool.Description) {
		t.Fatal("ToolHeader missing on a normal page load")
	}
	if strings.Contains(out, ui.EmbedStyle) {
		t.Fatal("embed stylesheet leaked into a normal page load")
	}
}

func TestToolPageDropsChromeInsideAFrame(t *testing.T) {
	out := renderPage(t, plainTool, true)
	if !strings.Contains(out, bodyMarker) {
		t.Fatal("tool body missing from the embedded page")
	}
	if strings.Contains(out, plainTool.Description) {
		t.Fatal("ToolHeader still rendered inside a frame")
	}
	if !strings.Contains(out, ui.EmbedStyle) {
		t.Fatal("embedded page did not get the padding stylesheet")
	}
	// The page shell itself must survive — the frame still needs the
	// stylesheet and theme class, only the chrome goes.
	if !strings.Contains(out, "/public/css/app.css") {
		t.Fatal("embedded page lost the layout shell")
	}
}

func TestFullScreenToolIgnoresEmbedMode(t *testing.T) {
	agents := tool.Tool{Key: "agents", Name: "Agents", Description: "Manage AI agent sessions", Icon: "*", Path: "/tools/agents", FullScreen: true}
	framed := renderPage(t, agents, true)
	if strings.Contains(framed, ui.EmbedStyle) {
		t.Fatal("embed mode rewrote a full-screen tool's layout; it owns its own chrome")
	}
	if framed != renderPage(t, agents, false) {
		t.Fatal("full-screen tool rendered differently inside a frame")
	}
}
