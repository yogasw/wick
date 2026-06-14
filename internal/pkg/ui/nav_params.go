package ui

import (
	"context"

	"github.com/yogasw/wick/internal/entity"
)

// NavParams holds per-user navigation visibility flags.
type NavParams struct {
	CanSeeAgents bool
}

var navParamsFn func(ctx context.Context, user *entity.User) NavParams

// SetNavParamsFn registers the hook that resolves per-user nav flags.
func SetNavParamsFn(fn func(ctx context.Context, user *entity.User) NavParams) {
	navParamsFn = fn
}

// NavParamsFor returns NavParams for the given user.
// CanSeeAgents is true only when the agents process is running AND navParamsFn grants access.
func NavParamsFor(ctx context.Context, user *entity.User) NavParams {
	if user == nil {
		return NavParams{}
	}
	if !agentsAvailable() {
		return NavParams{}
	}
	if navParamsFn == nil {
		return NavParams{}
	}
	return navParamsFn(ctx, user)
}
