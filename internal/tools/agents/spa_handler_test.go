package agents

import (
	"io/fs"
	"strings"
	"testing"
)

// TestSPAEmbedHasWorkflowApp asserts the Vite build artefact is reachable
// through the //go:embed tree. Failing here usually means the FE bundle
// wasn't built before running tests — fix: `cd fe && npm run build:workflow`.
func TestSPAEmbedHasWorkflowApp(t *testing.T) {
	sub, err := fs.Sub(SPAFS, "dist/workflow")
	if err != nil {
		t.Fatalf("fs.Sub(dist/workflow): %v", err)
	}
	idx, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v — did `npm run build:workflow` run?", err)
	}
	body := string(idx)
	// Spot-check the shell — the Vite-injected base URL must point at
	// the SPA mount, otherwise asset paths break in the browser.
	if !strings.Contains(body, "/tools/agents/agents-v2/workflow/") {
		t.Errorf("index.html missing /tools/agents/agents-v2/workflow/ base; got: %s", body)
	}
	// Sanity: at least one asset is referenced.
	if !strings.Contains(body, "/assets/") {
		t.Errorf("index.html missing /assets/ asset references; got: %s", body)
	}
}

// TestSPAEmbedAssetsTreeWalk asserts there's at least one .js asset under
// the embed — guarantees the Vite output actually shipped.
func TestSPAEmbedAssetsTreeWalk(t *testing.T) {
	sub, err := fs.Sub(SPAFS, "dist/workflow/assets")
	if err != nil {
		t.Skip("no built assets dir — skipping until fe/agents/workflow built")
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		t.Fatalf("read assets dir: %v", err)
	}
	var jsCount int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			jsCount++
		}
	}
	if jsCount == 0 {
		t.Errorf("expected at least one .js bundle in dist/workflow/assets; got %d entries", len(entries))
	}
}

// TestHasAssetExt covers the small predicate used by the SPA shell
// handler to decide whether an unmatched path is an asset 404 or a
// client-side route to be handed back the shell.
func TestHasAssetExt(t *testing.T) {
	cases := map[string]bool{
		"/foo/bar.js":        true,
		"/foo/bar.css":       true,
		"/foo/bar.map":       true,
		"/x.json":            true,
		"/icon.svg":          true,
		"/font.woff2":        true,
		"/edit/abc":          false,
		"/":                  false,
		"/some/deep/route":   false,
		"/edit/abc/withDot.": false,
	}
	for path, want := range cases {
		if got := hasAssetExt(path); got != want {
			t.Errorf("hasAssetExt(%q) = %v, want %v", path, got, want)
		}
	}
}
