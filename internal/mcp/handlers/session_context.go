package handlers

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/yogasw/wick/internal/login"
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

// sessionStamp is the stamped session plus whether the caller merely
// claimed it. unverified is set for a session read from the wire header
// of a credential that names none (a PAT, OAuth): such a session still
// routes and narrows the call, but lends nothing — see SessionTrusted.
type sessionStamp struct {
	id         string
	unverified bool
}

// WithSessionID stamps the session a call belongs to onto ctx. Callers
// outside the auth middleware (the in-process dispatcher) may set it
// directly — they resolve the session themselves and never read it from
// caller-supplied input.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionStamp{id: sessionID})
}

// WithUnverifiedSessionID stamps a session the caller named itself (the
// wire header under a credential that names no session). It routes the
// call exactly like WithSessionID, but SessionTrusted only accepts it
// when the principal owns that session.
func WithUnverifiedSessionID(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionStamp{id: sessionID, unverified: true})
}

// SessionIDFrom reads the stamped session, or "" when the call arrived
// without one (stdio, a PAT script, a spawn that predates the header).
func SessionIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(sessionCtxKey{}).(sessionStamp)
	return s.id
}

type sessionTrustAllKey struct{}

// WithSessionTrustAll marks ctx as wick's own principal (the per-boot
// internal token, the local stdio transport): every session it names is
// trusted, as those callers already hold the whole host.
func WithSessionTrustAll(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionTrustAllKey{}, true)
}

// SessionOwnerResolver maps a session id to the wick user who owns it.
type SessionOwnerResolver func(sessionID string) (userID string, ok bool)

var sessionOwnerResolver atomic.Pointer[SessionOwnerResolver]

// SetSessionOwnerResolver wires the session → owner lookup SessionTrusted
// uses. Set once at boot; nil turns ownership off, which leaves only
// sessions named by the credential itself trusted.
func SetSessionOwnerResolver(f SessionOwnerResolver) {
	if f == nil {
		sessionOwnerResolver.Store(nil)
		return
	}
	sessionOwnerResolver.Store(&f)
}

// SessionTrusted reports whether the caller on ctx may use what belongs
// to sessionID — the connectors its agent's owner lends, its
// session-workspace connectors, its owner's connected accounts. True for
// the session the credential itself named (or an in-process dispatcher
// stamped), for a session the principal owns, and for wick's own
// principal. A session named only by a header or a session_id argument
// is false: it may still narrow the caller, never widen them.
func SessionTrusted(ctx context.Context, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if ctx == nil || sessionID == "" {
		return false
	}
	if all, _ := ctx.Value(sessionTrustAllKey{}).(bool); all {
		return true
	}
	if s, _ := ctx.Value(sessionCtxKey{}).(sessionStamp); s.id == sessionID && !s.unverified {
		return true
	}
	user := login.GetUser(ctx)
	if user == nil || user.ID == "" {
		return false
	}
	f := sessionOwnerResolver.Load()
	if f == nil {
		return false
	}
	owner, ok := (*f)(sessionID)
	return ok && owner == user.ID
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
