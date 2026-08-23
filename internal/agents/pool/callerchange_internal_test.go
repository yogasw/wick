package pool

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

func ctxAs(userID string) context.Context {
	if userID == "" {
		return context.Background()
	}
	return login.WithUser(context.Background(),
		&entity.User{ID: userID, Role: entity.RoleUser, Approved: true}, nil)
}

func poolWithCaller() *Pool {
	return &Pool{cfg: PoolConfig{
		CallerUserID: func(ctx context.Context) string {
			if u := login.GetUser(ctx); u != nil {
				return u.ID
			}
			return ""
		},
	}}
}

// TestCallerChanged decides whether a live subprocess may serve this message.
// It must say yes only when both sides are known AND differ — a running
// process carries the previous caller's MCP credential in its argv, so reusing
// it for someone else would run their turn under the wrong identity.
func TestCallerChanged(t *testing.T) {
	p := poolWithCaller()

	t.Run("same user reuses the process", func(t *testing.T) {
		if p.callerChanged(ctxAs("user-a"), &runEntry{callerUserID: "user-a"}) {
			t.Fatal("same caller triggered a respawn")
		}
	})
	t.Run("different user forces a respawn", func(t *testing.T) {
		if !p.callerChanged(ctxAs("user-b"), &runEntry{callerUserID: "user-a"}) {
			t.Fatal("user-b would have run under user-a identity")
		}
	})
	t.Run("unknown incoming caller does not recycle", func(t *testing.T) {
		// A message with no resolved user gives nothing to compare; killing a
		// healthy process on a blank buys nothing.
		if p.callerChanged(ctxAs(""), &runEntry{callerUserID: "user-a"}) {
			t.Fatal("blank caller recycled a healthy process")
		}
	})
	t.Run("ownerless spawn is never recycled", func(t *testing.T) {
		// cron / system / legacy spawns have no identity to conflict with.
		if p.callerChanged(ctxAs("user-b"), &runEntry{callerUserID: ""}) {
			t.Fatal("ownerless spawn recycled on caller grounds")
		}
	})
	t.Run("nil entry is safe", func(t *testing.T) {
		if p.callerChanged(ctxAs("user-b"), nil) {
			t.Fatal("nil entry reported a caller change")
		}
	})
	t.Run("no resolver disables the check", func(t *testing.T) {
		bare := &Pool{}
		if bare.callerChanged(ctxAs("user-b"), &runEntry{callerUserID: "user-a"}) {
			t.Fatal("unwired resolver still reported a change")
		}
	})
}
