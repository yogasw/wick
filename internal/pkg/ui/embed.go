package ui

import (
	"context"
	"net/http"
	"strings"
)

// Embed mode strips wick's page chrome (navbar, tool header, setup
// banner) so a page can be dropped into an <iframe> on another tool
// and read as part of that page instead of as a second app nested
// inside the first.
//
// It is auto-detected, not opt-in: a navigation into a frame carries
// `Sec-Fetch-Dest: iframe`, so every browser that speaks Fetch Metadata
// (Chrome 80+, Firefox 90+, Safari 16.4+) gets the stripped shell on the
// first paint with no query string, and keeps it across in-frame links
// and form posts because the header rides on those requests too. The
// `?embed=` query parameter overrides the sniff in both directions —
// `embed=1` for a browser too old to send the header, `embed=0` to see
// the full page inside a frame (debugging).
//
// Full-screen tools (the agents console) are deliberately exempt: they
// already skip the shared chrome and render their own, which embed mode
// must not touch. That exemption lives in the renderer, not here.

type embeddedCtxKey struct{}

// WithEmbedded stamps the embed decision onto ctx. Set once per request
// by EmbedContext; components read it with EmbeddedFromContext.
func WithEmbedded(ctx context.Context, embedded bool) context.Context {
	return context.WithValue(ctx, embeddedCtxKey{}, embedded)
}

// EmbeddedFromContext reports whether the current request is being
// rendered inside a frame. False for any context that never passed
// through EmbedContext, so non-HTTP render paths keep full chrome.
func EmbeddedFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(embeddedCtxKey{}).(bool)
	return v
}

// DetectEmbedded decides embed mode for one request: an explicit
// ?embed= wins, otherwise the Fetch Metadata destination decides.
func DetectEmbedded(r *http.Request) bool {
	if raw := r.URL.Query().Get("embed"); raw != "" {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	// "frame" is the legacy <frame> destination; "embed"/"object" cover
	// the plugin elements. All of them mean "not the top-level page".
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest"))) {
	case "iframe", "frame", "embed", "object":
		return true
	}
	return false
}

// EmbedContext is the middleware that runs DetectEmbedded once and puts
// the answer in the request context, so no handler has to re-sniff it.
func EmbedContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithEmbedded(r.Context(), DetectEmbedded(r))))
	})
}

// EmbedStyle is the tiny stylesheet injected in place of the stripped
// chrome. Tool bodies get their top padding from the tool header they
// sit under; without it the first row would touch the frame edge. The
// bare element selector loses to any Tailwind utility class, so a body
// that sets its own padding keeps it.
const EmbedStyle = `<style>main{padding-top:1.25rem}</style>`
