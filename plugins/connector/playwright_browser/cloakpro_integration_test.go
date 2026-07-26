//go:build browser_integration

// Integration test proving the WICK launch path (Playwright-go + launchOptions)
// runs CloakBrowser Pro with the license key threaded through as env — the fix
// for the "target closed" flash. Gated behind browser_integration + skipped when
// CLOAKBROWSER_LICENSE_KEY isn't set, so it only runs where a real pro license
// is available (never in CI without a key).
//
// Run: CLOAKBROWSER_LICENSE_KEY=cb_xxx go test -tags browser_integration \
//        ./plugins/connector/playwright_browser/ -run TestIntegrationCloakPro
package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestIntegrationCloakProScrapesGoogle(t *testing.T) {
	if testing.Short() {
		t.Skip("browser integration test skipped in -short")
	}
	key := strings.TrimSpace(os.Getenv("CLOAKBROWSER_LICENSE_KEY"))
	if key == "" {
		t.Skip("CLOAKBROWSER_LICENSE_KEY not set — skipping pro launch test")
	}

	// Config exactly as the manager would save it for the pro engine: engine
	// selected + license key set. This drives resolveCloakBinary (→ the CLI's
	// ~/.cloakbrowser pro binary) and cloakLaunchEnv (→ the license in the
	// browser's env) through the real launchOptions path.
	cfg := map[string]string{
		"browser":           cloakProEngine,
		"headless":          "true",
		"cloak_license_key": key,
	}
	c := connector.NewCtx(context.Background(), "cloak-pro-test", cfg, map[string]string{
		"url":     "https://www.google.com/",
		"as_text": "false",
	}, nil, nil, nil)

	// getContent goes launchOptions → bt.Launch(cloak-pro binary + license env) →
	// goto → read. If the license env weren't passed, the pro binary would exit
	// and this would be "target closed".
	out, err := getContent(c)
	if err != nil {
		t.Fatalf("cloak-pro getContent(google) failed — license env not reaching launch? err=%v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", out)
	}
	html, _ := m["content"].(string)
	if html == "" {
		html, _ = m["text"].(string)
	}
	if !strings.Contains(strings.ToLower(html), "google") {
		t.Fatalf("expected google content, got %d chars: %.120s", len(html), html)
	}
	t.Logf("cloak-pro scraped google.com OK (%d chars)", len(html))
}
