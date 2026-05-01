package connector

import (
	"net/http"
	"time"
)

// DefaultHTTPTimeout is the per-request timeout wick uses for the
// http.Client it injects into Ctx. Connectors that need a different
// timeout can build their own *http.Client and call it via Ctx.HTTP
// only as a starting point — Ctx.HTTP is a field, not a method, so it
// can be replaced inside Execute when needed.
const DefaultHTTPTimeout = 30 * time.Second

// NewHTTPClient returns the *http.Client wick injects into Ctx by
// default. Exposed so tests and the panel-test handler can build a
// matching client without recreating the timeout policy.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: DefaultHTTPTimeout}
}
