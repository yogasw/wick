package handlers

import (
	"context"
	"net/http"
	"strings"
)

// session_context.go carries "which session is this call running in" the
// same way identity is already carried: on the request context, stamped
// once by the MCP auth middleware after the credential is verified.
//
// The X-Wick-Session-Id header remains the WIRE format — it is how a
// spawned CLI tells the HTTP transport which conversation it is — but it
// is no longer the thing handlers read. Two reasons that matters:
//
//   - A handler that needs the session had to be handed the whole
//     *http.Request just to reach one header, even when everything else
//     it does takes a context.
//   - The in-process path (wick's own engine) has no HTTP at all and was
//     inventing a fake request purely to have somewhere to put the
//     header.
//
// Stamped after verification, never before: a value a caller could set
// itself would be worth nothing for authorization.

type sessionCtxKey struct{}

// WithSessionID stamps the session a call belongs to onto ctx. Callers
// outside the auth middleware (the in-process dispatcher) may set it
// directly — they resolve the session themselves and never read it from
// caller-supplied input.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

// SessionIDFrom reads the stamped session, or "" when the call arrived
// without one (stdio, a PAT script, a spawn that predates the header).
func SessionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(sessionCtxKey{}).(string)
	return s
}

// SessionOf is what handlers call: the stamped session, falling back to
// the wire header for transports that do not pass through the auth
// middleware (stdio, tests). Never reads a caller-supplied argument —
// that is the callers' own decision, see CallSession and
// ResolveSessionPreferArg.
func SessionOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	if s := SessionIDFrom(r.Context()); s != "" {
		return s
	}
	return strings.TrimSpace(r.Header.Get(SessionHeader))
}
