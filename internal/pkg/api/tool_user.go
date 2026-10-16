package api

import (
	"context"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// approvedToolUser loads the wick user behind a verified credential (a PAT
// or wick's SSO JWT) as a tool.User. ok is false for an unknown or deleted
// user and for one not approved — checked per request, so revoking access
// takes effect on the next call.
func approvedToolUser(ctx context.Context, uid string,
	get func(context.Context, string) (*entity.User, error),
	tags func(context.Context, string) []string,
) (tool.User, bool) {
	u, err := get(ctx, uid)
	if err != nil || u == nil || !u.Approved {
		return tool.User{}, false
	}
	return tool.User{
		ID: u.ID, Name: u.Name, Email: u.Email, IsAdmin: u.IsAdmin(),
		Tags: tags(ctx, uid),
	}, true
}
