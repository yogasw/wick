package nodes

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workflow"
)

// A workflow-created session carries the workflow's author.
//
// Without this the session is born with nobody attached, and an ownerless
// session mints no per-user MCP credential: the spawn falls back to wick's
// internal principal, which holds no access tags. That is the whole reason an
// agent node looked like it had "no MCP" — not a limit of the node, but of a
// session nobody owned.
func TestSessionInitStampsWorkflowAuthorAsOwner(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	rc := newTestRC()
	rc.Workflow.CreatedBy = "user-ada"

	out, err := NewSessionInitExecutor(p).Execute(context.Background(),
		workflow.Node{ID: "session-init", Type: workflow.NodeSessionInit}, rc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sessionID, _ := out.Result.(string)
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		t.Fatalf("load %s: %v", sessionID, err)
	}
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q, want user-ada — an ownerless workflow session spawns with no access tags", sess.Meta.UserID)
	}
}

// An unattributed workflow leaves the session ownerless rather than guessing.
// The fallback is narrower access (the internal principal, no tags), never
// wider, so guessing would be the only way to get this wrong.
func TestSessionInitLeavesOwnerEmptyWhenWorkflowHasNoAuthor(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	rc := newTestRC() // CreatedBy deliberately empty

	out, err := NewSessionInitExecutor(p).Execute(context.Background(),
		workflow.Node{ID: "session-init", Type: workflow.NodeSessionInit}, rc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sessionID, _ := out.Result.(string)
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		t.Fatalf("load %s: %v", sessionID, err)
	}
	if sess.Meta.UserID != "" {
		t.Fatalf("owner = %q, want empty — nothing knows who to attribute this run to", sess.Meta.UserID)
	}
}

// A workflow pinned to a session somebody already owns must not take it over.
// Ownership decides whose access every later spawn uses, so moving it would
// hand a whole conversation to whoever wired the workflow.
func TestSessionInitDoesNotStealAnExistingOwner(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	rc := newTestRC()
	rc.Workflow.CreatedBy = "user-mallory"

	const pinned = "wf-pinned-session"
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: pinned, Origin: session.OriginUI, UserID: "user-ada",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	n := workflow.Node{ID: "session-init", Type: workflow.NodeSessionInit, SessionID: pinned}
	if _, err := NewSessionInitExecutor(p).Execute(context.Background(), n, rc); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := session.Load(layout, pinned)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("owner = %q, want user-ada — a workflow must not take over an owned session", sess.Meta.UserID)
	}
}
