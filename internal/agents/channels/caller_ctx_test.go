package channels

import (
	"context"
	"testing"
)

// resolveCaller mirrors the CallerUserID resolver the pool is wired with in
// api/server.go: a login session wins, and a channel dispatch (which has no
// login session at all) falls back to what the channel stamped.
//
// loginUser stands in for login.GetUser(ctx) — importing internal/login here
// would drag entity + the whole auth surface into a context test.
func resolveCaller(ctx context.Context, loginUser string) string {
	if loginUser != "" {
		return loginUser
	}
	return CallerUserID(ctx)
}

func TestCallerUserIDCtx_RoundTrip(t *testing.T) {
	base := context.Background()

	if got := CallerUserID(base); got != "" {
		t.Errorf("bare ctx caller = %q, want empty", got)
	}

	ctx := WithCallerUserID(base, "user-ada")
	if got := CallerUserID(ctx); got != "user-ada" {
		t.Errorf("caller = %q, want user-ada", got)
	}

	// Empty must not plant a key: the pool reads "" as "no caller to compare"
	// and a stored empty string would be indistinguishable from an absent one
	// while still shadowing an outer value.
	if got := CallerUserID(WithCallerUserID(ctx, "")); got != "user-ada" {
		t.Errorf("empty caller overwrote an existing one: got %q, want user-ada", got)
	}
}

// TestCallerUserIDCtx_ChannelDispatchIsNotCallerless pins the fix for the
// intermittent-identity bug.
//
// Channel dispatches are built from context.Background(), so they carry no
// login session. Before the channel stamped the resolved user itself, the
// pool's CallerUserID returned "" for every Slack message — which reads as
// "nothing to compare" in Pool.callerChanged, so a RUNNING subprocess was
// always reused. A process spawned before the session had an owner holds the
// synthetic-admin MCP token in its argv, and reuse kept serving that identity
// for the life of the thread.
func TestCallerUserIDCtx_ChannelDispatchIsNotCallerless(t *testing.T) {
	// No login session (channel dispatch), nothing stamped: callerless.
	if got := resolveCaller(context.Background(), ""); got != "" {
		t.Fatalf("unstamped channel dispatch caller = %q, want empty", got)
	}

	// Same dispatch, now stamped by the channel: the pool can compare, so a
	// process spawned for someone else is recycled instead of reused.
	ctx := WithCallerUserID(context.Background(), "user-ada")
	if got := resolveCaller(ctx, ""); got != "user-ada" {
		t.Fatalf("stamped channel dispatch caller = %q, want user-ada", got)
	}

	// A real login session still wins: the web UI's authenticated principal
	// must not be shadowed by a channel stamp riding along on the ctx.
	if got := resolveCaller(ctx, "user-bob"); got != "user-bob" {
		t.Fatalf("login caller = %q, want user-bob to win over the channel stamp", got)
	}
}
