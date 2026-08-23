package ui

import "context"

// impersonation.go carries the "you are viewing as someone else" flag from the
// session middleware to the layout.
//
// It exists because an admin who forgets they switched will misread everything
// on screen — thinking a permission is broken when they are simply looking at a
// less-privileged account. The banner is the correction, and it has to appear on
// every page, so the flag rides the request context rather than each handler
// remembering to pass it.

type impersonationCtxKey struct{}

// ImpersonationInfo describes an active "view as" session.
type ImpersonationInfo struct {
	// Active is true while the session belongs to an impersonated user.
	Active bool
	// ActingAs is the impersonated user's display name or email, so the banner
	// names who the admin currently is.
	ActingAs string
}

// WithImpersonation stamps the flag onto the request context.
func WithImpersonation(ctx context.Context, info ImpersonationInfo) context.Context {
	return context.WithValue(ctx, impersonationCtxKey{}, info)
}

// ImpersonationFromContext reads the flag. The zero value (not impersonating)
// is returned when nothing was stamped, so callers need no nil handling.
func ImpersonationFromContext(ctx context.Context) ImpersonationInfo {
	if v, ok := ctx.Value(impersonationCtxKey{}).(ImpersonationInfo); ok {
		return v
	}
	return ImpersonationInfo{}
}
