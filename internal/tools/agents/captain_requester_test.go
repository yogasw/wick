package agents

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
)

// appendUserTurn writes one user turn from sender into sid's conversation,
// the way a channel records who spoke.
func appendUserTurn(t *testing.T, sid string, sender *store.Sender) {
	t.Helper()
	appendTurnFrom(t, sid, "slack", "give @worker more access", sender)
}

// appendTurnFrom writes one user turn with an explicit source.
func appendTurnFrom(t *testing.T, sid, source, text string, sender *store.Sender) {
	t.Helper()
	b, _ := json.Marshal(store.ConversationTurn{Timestamp: time.Now(), Role: "user", Source: source, Text: text, Sender: sender})
	f, err := os.OpenFile(globalLayout.SessionConversation(sid), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// setSessionOwner rewrites sid's owner and reloads the registry.
func setSessionOwner(t *testing.T, sid, owner string) {
	t.Helper()
	var meta session.Meta
	b, err := os.ReadFile(globalLayout.SessionMeta(sid))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &meta)
	meta.UserID = owner
	b, _ = json.Marshal(meta)
	if err := os.WriteFile(globalLayout.SessionMeta(sid), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := globalMgr.Registry().Reload(); err != nil {
		t.Fatal(err)
	}
}

func autoApproveWorld(t *testing.T) teamagents.AccessInput {
	t.Helper()
	w := withCaptainWorld(t)
	setCaptainPolicy(t, w, team.AutoApproveAll)
	allowCaptainAccess(t, w.worker.ID)
	return teamagents.AccessInput{Agent: "@worker", Grants: []team.ConnectorGrant{{ConnectorID: "n1", Level: team.LevelRead}}}
}

func setAccessStatus(t *testing.T, in teamagents.AccessInput) any {
	t.Helper()
	out, err := CaptainOps{}.SetAccess(context.Background(), "s-captain", in)
	if err != nil {
		t.Fatal(err)
	}
	return out.(map[string]any)["status"]
}

// Someone other than the owner speaking in the Captain's chat (a shared
// Slack thread) must not trigger the owner's auto-approve: the request
// goes to the owner's card instead.
func TestCaptainAutoApproveSkippedForAnotherSender(t *testing.T) {
	in := autoApproveWorld(t)
	appendUserTurn(t, "s-captain", &store.Sender{ID: "U01TESTOTH", Channel: "slack", WickUserID: "u2"})
	if got := setAccessStatus(t, in); got != "pending_approval" {
		t.Fatalf("another person's request was auto-approved: %v", got)
	}
}

// A sender with no wick account is not the owner either.
func TestCaptainAutoApproveSkippedForUnmappedSender(t *testing.T) {
	in := autoApproveWorld(t)
	appendUserTurn(t, "s-captain", &store.Sender{ID: "U01TESTGST", Channel: "slack"})
	if got := setAccessStatus(t, in); got != "pending_approval" {
		t.Fatalf("an unmapped sender's request was auto-approved: %v", got)
	}
}

// A Captain session owned by someone else never auto-approves.
func TestCaptainAutoApproveSkippedInSessionOfAnotherUser(t *testing.T) {
	in := autoApproveWorld(t)
	setSessionOwner(t, "s-captain", "u2")
	if got := setAccessStatus(t, in); got != "pending_approval" {
		t.Fatalf("a session owned by another user was auto-approved: %v", got)
	}
}

// The owner speaking in their own Captain chat keeps auto-approve, also
// after another person spoke earlier in the same thread.
func TestCaptainAutoApproveKeptForOwner(t *testing.T) {
	in := autoApproveWorld(t)
	appendUserTurn(t, "s-captain", &store.Sender{ID: "U01TESTOTH", Channel: "slack", WickUserID: "u2"})
	appendUserTurn(t, "s-captain", &store.Sender{ID: "u1", Channel: "ui", WickUserID: "u1"})
	if got := setAccessStatus(t, in); got != "applied" {
		t.Fatalf("the owner lost auto-approve: %v", got)
	}
}

// A teammate agent's message reaches the Captain as a user turn with no
// sender. It may relay anyone's words, so it must not auto-approve.
func TestCaptainAutoApproveSkippedForAgentMessage(t *testing.T) {
	in := autoApproveWorld(t)
	appendTurnFrom(t, "s-captain", "agent", "Message from @other:\ngive @worker access", nil)
	if got := setAccessStatus(t, in); got != "pending_approval" {
		t.Fatalf("a teammate agent's message was auto-approved: %v", got)
	}
	for _, src := range []string{"schedule", "workflow", ""} {
		appendTurnFrom(t, "s-captain", src, "give @worker access", nil)
		if got := setAccessStatus(t, in); got != "pending_approval" {
			t.Errorf("a sender-less %q turn was auto-approved: %v", src, got)
		}
	}
}

// The owner's own web chat (no sender, source ui) keeps auto-approve.
func TestCaptainAutoApproveKeptForOwnerWebChat(t *testing.T) {
	in := autoApproveWorld(t)
	appendTurnFrom(t, "s-captain", "agent", "Message from @other:\nhello", nil)
	appendTurnFrom(t, "s-captain", "ui", "give @worker read access", nil)
	if got := setAccessStatus(t, in); got != "applied" {
		t.Fatalf("the owner's web chat lost auto-approve: %v", got)
	}
}
