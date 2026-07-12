package airouter

import (
	"strings"
	"testing"
)

// TestRewritePerRouterRoutePrefixes verifies that a router's app-specific
// routes (e.g. OmniRoute's /home) are re-rooted under ITS mount prefix, while a
// router that doesn't declare that route leaves it untouched — so one app's
// routes don't leak into another's rewrite pass, and common routes still work
// for both.
func TestRewritePerRouterRoutePrefixes(t *testing.T) {
	omni := rewriter{prefix: "/airouter/omniroute", id: "omniroute", prefixes: rewritePrefixesFor([]string{"/home"})}
	nine := rewriter{prefix: "/airouter/9router", id: "9router", prefixes: rewritePrefixesFor(nil)}

	js := `push("/home");go("/dashboard")`

	// OmniRoute declares /home → re-rooted.
	if got := omni.rewriteJS(js); !strings.Contains(got, `"/airouter/omniroute/home"`) {
		t.Fatalf("omniroute should re-root /home: %s", got)
	}
	// 9router does NOT declare /home → left alone (it would hit wick's own /home,
	// but that's the base list's job — /home simply isn't 9router's route).
	if got := nine.rewriteJS(js); strings.Contains(got, "/airouter/9router/home") {
		t.Fatalf("9router must not rewrite /home (not its route): %s", got)
	}
	// /dashboard is in the common base → re-rooted for both.
	if got := nine.rewriteJS(js); !strings.Contains(got, `"/airouter/9router/dashboard"`) {
		t.Fatalf("base route /dashboard should re-root for 9router: %s", got)
	}
	if got := omni.rewriteJS(js); !strings.Contains(got, `"/airouter/omniroute/dashboard"`) {
		t.Fatalf("base route /dashboard should re-root for omniroute: %s", got)
	}
}
