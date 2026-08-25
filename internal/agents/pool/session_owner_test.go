package pool

import (
	"context"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// ownerTestPool builds a pool over a temp layout with a session on disk.
func ownerTestPool(t *testing.T, sessionID string) (*Pool, agentconfig.Layout, *[]string) {
	t.Helper()
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     sessionID,
		Origin: session.OriginSlack,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	var refreshed []string
	p := &Pool{cfg: PoolConfig{
		Layout: layout,
		OnSessionMeta: func(id string) {
			refreshed = append(refreshed, id)
		},
	}}
	return p, layout, &refreshed
}

// TestEnsureSessionOwner_RefreshesRegistry reproduces the reported bug:
// wick_me reported "wick-agent-internal" from Slack even though the sender was
// a real user, and it kept doing so on retries.
//
// The owner was written to DISK correctly, but the per-spawn MCP credential is
// minted from the IN-MEMORY registry. With no refresh, the registry kept serving
// an ownerless session, so every spawn fell back to the shared internal token
// and ran as the synthetic admin. Because the disk was right, retrying could
// never fix it — only a server restart would.
func TestEnsureSessionOwner_RefreshesRegistry(t *testing.T) {
	p, layout, refreshed := ownerTestPool(t, "slack-thread-1")

	p.EnsureSessionOwner(context.Background(), "slack-thread-1", "user-ada")

	sess, err := session.Load(layout, "slack-thread-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner on disk = %q, want user-ada", sess.Meta.UserID)
	}
	if len(*refreshed) != 1 || (*refreshed)[0] != "slack-thread-1" {
		t.Fatalf("registry refresh = %v; without it the token minter keeps reading "+
			"an ownerless session and the agent stays the synthetic admin", *refreshed)
	}
}

// TestEnsureSessionOwner_IdempotentAndNoSpuriousRefresh: the Slack path now
// calls this on EVERY message to backfill older threads, so a repeat must not
// overwrite the owner or fire a refresh it does not need.
func TestEnsureSessionOwner_IdempotentAndNoSpuriousRefresh(t *testing.T) {
	p, layout, refreshed := ownerTestPool(t, "s1")
	ctx := context.Background()

	p.EnsureSessionOwner(ctx, "s1", "user-ada")
	if len(*refreshed) != 1 {
		t.Fatalf("first call refreshes = %d, want 1", len(*refreshed))
	}

	// A different user messaging the same session must NOT steal ownership.
	p.EnsureSessionOwner(ctx, "s1", "user-bob")

	sess, _ := session.Load(layout, "s1")
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q; a later sender took over the session", sess.Meta.UserID)
	}
	if len(*refreshed) != 1 {
		t.Fatalf("refresh fired %d times; an owned session needs no further refresh", len(*refreshed))
	}
}

// TestEnsureSessionOwner_IgnoresBlanksAndMissing keeps the every-message call
// cheap and safe: nothing to record must not touch disk or fire a refresh.
func TestEnsureSessionOwner_IgnoresBlanksAndMissing(t *testing.T) {
	p, _, refreshed := ownerTestPool(t, "s1")
	ctx := context.Background()

	p.EnsureSessionOwner(ctx, "", "user-ada")    // no session id
	p.EnsureSessionOwner(ctx, "s1", "")          // no user id
	p.EnsureSessionOwner(ctx, "ghost", "user-a") // session not on disk

	if len(*refreshed) != 0 {
		t.Fatalf("refreshed %v for inputs with nothing to record", *refreshed)
	}
}

// TestEnsureSessionOwner_NilHookIsSafe: the callback is optional, and tests or
// embedders may not wire it.
func TestEnsureSessionOwner_NilHookIsSafe(t *testing.T) {
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "s1"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	p := &Pool{cfg: PoolConfig{Layout: layout}}

	p.EnsureSessionOwner(context.Background(), "s1", "user-ada")

	sess, _ := session.Load(layout, "s1")
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q, want user-ada", sess.Meta.UserID)
	}
}
