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
// calls this on EVERY message to backfill older threads, so a repeat from the
// same person must not overwrite the owner or fire a refresh it does not need.
//
// A DIFFERENT sender is not a repeat — they join Participants, which the
// registry has to serve, so that one does refresh (see
// TestEnsureSessionOwner_RecordsEveryParticipant).
func TestEnsureSessionOwner_IdempotentAndNoSpuriousRefresh(t *testing.T) {
	p, layout, refreshed := ownerTestPool(t, "s1")
	ctx := context.Background()

	p.EnsureSessionOwner(ctx, "s1", "user-ada")
	if len(*refreshed) != 1 {
		t.Fatalf("first call refreshes = %d, want 1", len(*refreshed))
	}

	// Same user again: nothing to record.
	p.EnsureSessionOwner(ctx, "s1", "user-ada")

	// A different user messaging the same session must NOT steal ownership.
	p.EnsureSessionOwner(ctx, "s1", "user-bob")

	sess, _ := session.Load(layout, "s1")
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q; a later sender took over the session", sess.Meta.UserID)
	}
	if len(*refreshed) != 2 {
		t.Fatalf("refreshes = %v; want one for the owner stamp and one for bob joining", *refreshed)
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

// TestEnsureSessionOwner_RecordsEveryParticipant: the owner must not change
// hands, but a second person speaking in the same thread IS part of that
// conversation — that is what puts the thread in their own "Yours" list and
// keeps it openable by them.
func TestEnsureSessionOwner_RecordsEveryParticipant(t *testing.T) {
	p, layout, refreshed := ownerTestPool(t, "s1")
	ctx := context.Background()

	p.EnsureSessionOwner(ctx, "s1", "user-ada")
	p.EnsureSessionOwner(ctx, "s1", "user-bob")
	p.EnsureSessionOwner(ctx, "s1", "user-ada") // ada speaks again

	sess, _ := session.Load(layout, "s1")
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q, want user-ada (first writer keeps the session)", sess.Meta.UserID)
	}
	want := []string{"user-ada", "user-bob"}
	if got := sess.Meta.People(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("participants = %v, want %v", got, want)
	}
	if !sess.Meta.IsParticipant("user-bob") {
		t.Fatal("bob spoke in the session but is not a participant, so it never reaches his list")
	}
	if !sess.Meta.Shared() {
		t.Fatal("two speakers must read as shared — that is the multi-user marker in the UI")
	}
	// One refresh for the owner stamp, one for bob joining; ada's repeat
	// must not fire a third (the Slack path calls this on EVERY message).
	if len(*refreshed) != 2 {
		t.Fatalf("registry refreshes = %d (%v), want 2", len(*refreshed), *refreshed)
	}
}

// TestEnsureSessionOwner_BackfillsLegacyOwnerAsFirstParticipant: sessions
// created before Participants existed carry only UserID. The owner must stay
// first in the list, not end up behind whoever happens to speak next.
func TestEnsureSessionOwner_BackfillsLegacyOwnerAsFirstParticipant(t *testing.T) {
	p, layout, _ := ownerTestPool(t, "s1")
	ctx := context.Background()

	// Legacy shape: owner on disk, no participants.
	sess, _ := session.Load(layout, "s1")
	sess.Meta.UserID = "user-ada"
	sess.Meta.Participants = nil
	if err := session.SaveMeta(layout, "s1", sess.Meta); err != nil {
		t.Fatalf("save legacy meta: %v", err)
	}

	p.EnsureSessionOwner(ctx, "s1", "user-bob")

	sess, _ = session.Load(layout, "s1")
	got := sess.Meta.People()
	if len(got) != 2 || got[0] != "user-ada" || got[1] != "user-bob" {
		t.Fatalf("participants = %v, want [user-ada user-bob]", got)
	}
}

// TestEnsureSession_StampsOwnerAtCreate is the fix for the original bug: the
// FIRST message of a new Slack thread created an ownerless session, because
// the channel's EnsureSessionOwner call runs before the session exists. That
// left the thread out of the sender's list AND made its first spawn fall back
// to the shared internal token (synthetic admin) for the life of the process.
func TestEnsureSession_StampsOwnerAtCreate(t *testing.T) {
	layout := agentconfig.Layout{BaseDir: t.TempDir()}
	p := &Pool{cfg: PoolConfig{
		Layout: layout,
		CallerUserID: func(ctx context.Context) string {
			return "user-ada"
		},
	}}

	if err := p.EnsureSession(context.Background(), "slack-new-thread", "slack", ""); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	sess, err := session.Load(layout, "slack-new-thread")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q, want user-ada — an ownerless new thread spawns as the synthetic admin", sess.Meta.UserID)
	}
	if !sess.Meta.IsParticipant("user-ada") {
		t.Fatal("creator must be the first participant")
	}
	if sess.Meta.Shared() {
		t.Fatal("a one-person session must not carry the shared marker")
	}
}
