package wickmanager

import (
	"context"
	"errors"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/processctl"
)

var (
	errAccessDenied         = errors.New("access denied")
	errSystemUnavailable    = errors.New("system management unavailable in this run mode (start wick via the tray)")
	errNotAuthenticated     = errors.New("not authenticated")
	errLockedRow            = errors.New("config row is locked and cannot be edited")
	errCannotRegenerate     = errors.New("config row cannot be regenerated")
	errRequiredEmpty        = errors.New("required field cannot be empty")
)

// userFromCtx pulls the authenticated user off the request context.
// MCP tools/call middleware always populates this; tests that bypass
// the middleware should call login.WithUser before reaching ops.
func userFromCtx(ctx context.Context) *entity.User {
	return login.GetUser(ctx)
}

// requireUser returns the user or errNotAuthenticated. Most ops need
// at minimum an authenticated identity even when no admin or tag gate
// applies; call this first before deciding what else to check.
func requireUser(ctx context.Context) (*entity.User, error) {
	u := userFromCtx(ctx)
	if u == nil {
		return nil, errNotAuthenticated
	}
	return u, nil
}

// requireAdmin returns the user and asserts they carry the admin role.
// Used by every app_* op and by all system_* ops.
func requireAdmin(ctx context.Context) (*entity.User, error) {
	u, err := requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if !u.IsAdmin() {
		return nil, errAccessDenied
	}
	return u, nil
}

// requireTray returns errSystemUnavailable when the process was not
// launched via the tray. system_* ops chain this on top of
// requireAdmin so an admin running `wick server` can't accidentally
// stop the process they're talking to.
func requireTray() error {
	if !processctl.IsManaged() {
		return errSystemUnavailable
	}
	return nil
}
