package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// markerHandler records which branch of webhookBypass served a request.
func markerHandler(hit *string, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hit = name
		w.WriteHeader(http.StatusOK)
	})
}

func TestWebhookBypassRouting(t *testing.T) {
	cases := []struct {
		name, path, want string
		prefixes         []string
	}{
		{
			name:     "group prefix itself is exempt",
			path:     "/tools/hooks/webhook",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "open",
		},
		{
			name:     "descendant of the group is exempt",
			path:     "/tools/hooks/webhook/receive",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "open",
		},
		{
			name:     "deep descendant is exempt",
			path:     "/tools/hooks/webhook/a/b/c",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "open",
		},
		{
			name:     "tool root stays gated",
			path:     "/tools/hooks",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "gated",
		},
		{
			name:     "sibling route stays gated",
			path:     "/tools/hooks/settings",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "gated",
		},
		{
			// The regression this guards: strings.HasPrefix alone would
			// treat this as inside the group and silently drop its access
			// check, exposing an admin route because the name happens to
			// start with the group's.
			name:     "prefix-sharing sibling stays gated",
			path:     "/tools/hooks/webhookadmin",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "gated",
		},
		{
			name:     "prefix-sharing sibling subtree stays gated",
			path:     "/tools/hooks/webhook-internal/purge",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "gated",
		},
		{
			name:     "another tool's matching path stays gated",
			path:     "/tools/other/webhook/receive",
			prefixes: []string{"/tools/hooks/webhook"},
			want:     "gated",
		},
		{
			name:     "no prefixes means everything is gated",
			path:     "/tools/hooks/webhook/receive",
			prefixes: nil,
			want:     "gated",
		},
		{
			name:     "second of several prefixes matches",
			path:     "/tools/b/callback/x",
			prefixes: []string{"/tools/a/webhook", "/tools/b/callback"},
			want:     "open",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hit string
			h := webhookBypass(tc.prefixes,
				markerHandler(&hit, "open"),
				markerHandler(&hit, "gated"),
			)
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			h.ServeHTTP(httptest.NewRecorder(), req)
			if hit != tc.want {
				t.Fatalf("path %q served by %q, want %q", tc.path, hit, tc.want)
			}
		})
	}
}

// TestWebhookBypassCopiesPrefixes pins that mutating the caller's slice
// after construction cannot widen the exemption.
func TestWebhookBypassCopiesPrefixes(t *testing.T) {
	prefixes := []string{"/tools/hooks/webhook"}
	var hit string
	h := webhookBypass(prefixes,
		markerHandler(&hit, "open"),
		markerHandler(&hit, "gated"),
	)

	prefixes[0] = "/tools"

	req := httptest.NewRequest(http.MethodGet, "/tools/other/page", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if hit != "gated" {
		t.Fatalf("mutating the caller's slice widened the exemption: got %q", hit)
	}
}
