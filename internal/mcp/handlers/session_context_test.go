package handlers

import (
	"net/http/httptest"
	"testing"
)

// The context is the carrier; the header is only the wire format, so a
// header that disagrees with the verified session loses.
func TestSessionOf_ContextWinsOverHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set(SessionHeader, "sess-from-wire")
	r = r.WithContext(WithSessionID(r.Context(), "sess-verified"))

	if got := SessionOf(r); got != "sess-verified" {
		t.Fatalf("SessionOf = %q, want sess-verified", got)
	}
}

// Transports that never pass the auth middleware (stdio, tests) still
// resolve their session from the wire.
func TestSessionOf_FallsBackToHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set(SessionHeader, " sess-from-wire ")
	if got := SessionOf(r); got != "sess-from-wire" {
		t.Fatalf("SessionOf = %q, want sess-from-wire", got)
	}
}

func TestSessionOf_NothingAnywhere(t *testing.T) {
	if got := SessionOf(httptest.NewRequest("POST", "/mcp", nil)); got != "" {
		t.Fatalf("SessionOf = %q, want empty", got)
	}
	if got := SessionOf(nil); got != "" {
		t.Fatalf("SessionOf(nil) = %q, want empty", got)
	}
}

// A blank must not shadow a value stamped earlier in the chain.
func TestWithSessionID_IgnoresBlank(t *testing.T) {
	ctx := WithSessionID(httptest.NewRequest("POST", "/mcp", nil).Context(), "sess-abc")
	if got := SessionIDFrom(WithSessionID(ctx, "   ")); got != "sess-abc" {
		t.Fatalf("SessionIDFrom = %q, want sess-abc", got)
	}
}
