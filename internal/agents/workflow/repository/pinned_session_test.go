package repository

import (
	"strings"
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

func draftPinning(sessionID string) wf.Workflow {
	return wf.Workflow{
		ID: "it-ops",
		Graph: wf.Graph{Nodes: []wf.Node{
			{ID: "a", Type: wf.NodeAgent},
			{ID: "b", Type: wf.NodeSessionInit, SessionID: sessionID},
		}},
	}
}

// Pinning a session is borrowing whoever owns it: every agent turn in that
// session runs with the session's identity, so a workflow aimed at an admin's
// session would have its nodes served with the admin's connectors. Schedules
// have always been gated this way; workflows were not.
func TestSaveDraftRefusesASessionTheAuthorCannotReach(t *testing.T) {
	r := New(nil).WithSessionAccess(func(userID, sessionID string) bool {
		return !(userID == "user-mallory" && sessionID == "admin-session")
	})

	if got := r.checkPinnedSessions(draftPinning("admin-session"), "user-mallory"); got == nil {
		t.Fatal("saved a workflow pinned to a session the author cannot reach")
	} else if !strings.Contains(got.Error(), "admin-session") {
		t.Fatalf("error = %q, want it to name the session so the author can fix it", got)
	}

	if err := r.checkPinnedSessions(draftPinning("own-session"), "user-mallory"); err != nil {
		t.Fatalf("own session refused: %v", err)
	}
}

// Nodes that let wick choose the id carry no SessionID, so there is nobody
// else's session to borrow and nothing to check.
func TestSaveDraftIgnoresNodesThatPinNothing(t *testing.T) {
	r := New(nil).WithSessionAccess(func(string, string) bool { return false })
	if err := r.checkPinnedSessions(draftPinning(""), "user-ada"); err != nil {
		t.Fatalf("unpinned nodes were checked: %v", err)
	}
}

// No gate wired (tests, CLI) and no author resolved must both behave exactly
// as before — refusing every pinned session there would break working setups
// to enforce an attribution nobody can supply.
func TestSaveDraftSkipsTheCheckWhenItCannotJudge(t *testing.T) {
	if err := New(nil).checkPinnedSessions(draftPinning("admin-session"), "user-mallory"); err != nil {
		t.Fatalf("no gate wired: %v", err)
	}
	r := New(nil).WithSessionAccess(func(string, string) bool { return false })
	if err := r.checkPinnedSessions(draftPinning("admin-session"), ""); err != nil {
		t.Fatalf("no author resolved: %v", err)
	}
}
