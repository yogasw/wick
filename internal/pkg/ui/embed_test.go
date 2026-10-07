package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectEmbedded(t *testing.T) {
	cases := []struct {
		name string
		url  string
		dest string
		want bool
	}{
		{"plain page", "/tools/convert-text", "document", false},
		{"no fetch metadata", "/tools/convert-text", "", false},
		{"iframe navigation", "/tools/convert-text", "iframe", true},
		{"legacy frame", "/tools/convert-text", "frame", true},
		{"embed element", "/tools/convert-text", "embed", true},
		{"header case is ignored", "/tools/convert-text", "IFrame", true},
		{"query forces on for old browsers", "/tools/convert-text?embed=1", "document", true},
		{"query forces off inside a frame", "/tools/convert-text?embed=0", "iframe", false},
		{"unknown query value falls back to the header", "/tools/convert-text?embed=maybe", "iframe", true},
		{"other query params are ignored", "/tools/convert-text?text=hi", "document", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.dest != "" {
				r.Header.Set("Sec-Fetch-Dest", tc.dest)
			}
			if got := DetectEmbedded(r); got != tc.want {
				t.Fatalf("DetectEmbedded = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmbeddedFromContextDefaultsFalse(t *testing.T) {
	if EmbeddedFromContext(context.Background()) {
		t.Fatal("a context that never saw EmbedContext must not report embedded")
	}
	if !EmbeddedFromContext(WithEmbedded(context.Background(), true)) {
		t.Fatal("WithEmbedded(true) did not round-trip")
	}
}

func TestEmbedContextStampsRequest(t *testing.T) {
	var got, ok bool
	h := EmbedContext(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = EmbeddedFromContext(r.Context())
		ok = true
	}))
	r := httptest.NewRequest(http.MethodGet, "/tools/convert-text", nil)
	r.Header.Set("Sec-Fetch-Dest", "iframe")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !ok {
		t.Fatal("middleware did not call the next handler")
	}
	if !got {
		t.Fatal("iframe request reached the handler without the embed flag")
	}
}
