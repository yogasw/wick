//go:build browser_integration

// Integration test for the FREE CloakBrowser engine launch path. Unlike the pro
// test it needs no license (the free v146 binary is one concurrent session, no
// key), so it exercises the older-binary branch of the 1:1 Python mirror:
//   - launchOptions: Headless left to Playwright (no manual --headless flag),
//     IgnoreDefaultArgs + cloakArgs (--no-sandbox, --fingerprint, windows profile,
//     --ignore-gpu-blocklist on Windows, NO --start-maximized below v148).
//   - contextOptions: a fixed 1920x947 viewport (v146 does not report coherent
//     headless dimensions, so no_viewport is NOT used here) — the wrapper's
//     _resolve_context_viewport for a sub-148 headless binary.
//
// It resolves the binary via CLOAKBROWSER_FREE_BINARY (an explicit chrome path)
// so it runs against the already-downloaded ~/.cloakbrowser free build without a
// GitHub round-trip. Skipped when that env isn't set.
//
// Run: CLOAKBROWSER_FREE_BINARY="$HOME/.cloakbrowser/chromium-146.0.7680.177.5/chrome.exe" \
//        go test -tags browser_integration ./plugins/connector/playwright_browser/ \
//        -run TestIntegrationCloakFree
package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestIntegrationCloakFreeScrapesGoogle(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration test skipped in -short")
	}
	bin := strings.TrimSpace(os.Getenv("CLOAKBROWSER_FREE_BINARY"))
	if bin == "" {
		t.Skip("CLOAKBROWSER_FREE_BINARY not set — skipping free launch test")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("CLOAKBROWSER_FREE_BINARY %q not found: %v", bin, err)
	}

	// Free engine + an explicit executable_path so no download is needed. This
	// drives the exact launchOptions/contextOptions the manager would use, minus
	// the GitHub asset resolution.
	cfg := map[string]string{
		"browser":         cloakEngine,
		"headless":        "true",
		"executable_path": bin,
	}
	c := connector.NewCtx(context.Background(), "cloak-free-test", cfg, map[string]string{
		"url":     "https://www.google.com/",
		"as_text": "false",
	}, nil, nil, nil)

	out, err := getContent(c)
	if err != nil {
		t.Fatalf("cloak-free getContent(google) failed: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", out)
	}
	html, _ := m["content"].(string)
	if !strings.Contains(strings.ToLower(html), "google") {
		t.Fatalf("expected google content, got %d chars: %.120s", len(html), html)
	}
	t.Logf("cloak-free scraped google.com OK (%d chars)", len(html))
}
