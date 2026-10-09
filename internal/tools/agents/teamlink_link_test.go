package agents

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// The pair of a caller's conversation and a target chat lives on the
// target chat's meta: written to disk, read back, moved by a re-pair, and
// found from the target's side for a reply back.
func TestPoolTurnsLinkedChat(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	seedTeamProject(t, "p2", owner.ID)
	anton := seedTeamAgent(t, owner.ID, "anton", "p1")
	captain := seedTeamAgent(t, owner.ID, "captain", "p2")
	ctx := context.Background()
	turns := poolTurns{}
	peer := teamlink.Peer{ID: anton.ID, OwnerID: owner.ID, Handle: "anton"}
	capPeer := teamlink.Peer{ID: captain.ID, OwnerID: owner.ID, Handle: "captain"}

	capChat := openChat(t, owner, captain.ID, false)
	openChat(t, owner, anton.ID, false) // anton's main
	first := openChat(t, owner, anton.ID, true)
	second := openChat(t, owner, anton.ID, true)

	if got := turns.LinkedChat(ctx, peer, capChat); got != "" {
		t.Fatalf("unpaired = %q", got)
	}
	if err := turns.Link(ctx, peer, first, capChat); err != nil {
		t.Fatal(err)
	}
	if got := turns.LinkedChat(ctx, peer, capChat); got != first {
		t.Fatalf("paired = %q, want %q", got, first)
	}
	// On disk, so a restart reads it back.
	if s, err := session.Load(globalLayout, first); err != nil || s.Meta.LinkedFromSession != capChat {
		t.Fatalf("meta on disk = %+v, %v", s.Meta.LinkedFromSession, err)
	}
	// A reply from anton's paired chat back to the captain lands in the
	// captain's asking chat.
	if got := turns.LinkedChat(ctx, capPeer, first); got != capChat {
		t.Fatalf("reply back = %q, want %q", got, capChat)
	}
	// Re-pairing (new_chat) moves the pair.
	if err := turns.Link(ctx, peer, second, capChat); err != nil {
		t.Fatal(err)
	}
	if got := turns.LinkedChat(ctx, peer, capChat); got != second {
		t.Fatalf("re-paired = %q, want %q", got, second)
	}
	if s, _ := session.Load(globalLayout, first); s.Meta.LinkedFromSession != "" {
		t.Fatalf("old chat still paired: %q", s.Meta.LinkedFromSession)
	}
}

// P13 + P14: a teammate's chats as the caller's LLM sees them are only the
// chat user's own — a recipient never gets the owner's chat (history off
// or on), the owner never gets a recipient's, even when that chat claims
// to be paired with the asking conversation.
func TestPoolTurnsChatsOwnOnlyWhateverHistoryToggle(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	ownerChat := openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, bob.ID); code != 200 {
		t.Fatalf("share: %d", code)
	}
	bobChat := openChat(t, bob, p.ID, false)
	ctx := context.Background()
	turns := poolTurns{}
	// The owner's chat claims bob's caller conversation.
	if err := turns.Link(ctx, teamlink.Peer{ID: p.ID, OwnerID: owner.ID, Handle: "helper"}, ownerChat, "bob-caller"); err != nil {
		t.Fatal(err)
	}
	ids := func(peer teamlink.Peer) []string {
		var out []string
		for _, c := range turns.Chats(ctx, peer) {
			out = append(out, c.SessionID)
		}
		return out
	}
	asBob := teamlink.Peer{ID: p.ID, OwnerID: owner.ID, Handle: "helper", ChatUser: bob.ID}
	asOwner := teamlink.Peer{ID: p.ID, OwnerID: owner.ID, Handle: "helper"}
	for _, on := range []bool{true, false} {
		if code := setShareHistory(t, owner, p.ID, bob.ID, on); code != 200 {
			t.Fatalf("toggle %v: %d", on, code)
		}
		if got := ids(asBob); len(got) != 1 || got[0] != bobChat {
			t.Fatalf("history %v: bob sees %v, want only %s", on, got, bobChat)
		}
		if got := ids(asOwner); len(got) != 1 || got[0] != ownerChat {
			t.Fatalf("history %v: owner sees %v, want only %s", on, got, ownerChat)
		}
	}
}

func TestSlackThreadLink(t *testing.T) {
	if got := slackThreadLink(slackremote.State{}); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := slackThreadLink(slackremote.State{Channel: "C1", ThreadTS: "1700000000.123456"}); got != "https://slack.com/archives/C1/p1700000000123456" {
		t.Fatalf("link = %q", got)
	}
}

// Link rewrites the whole meta file, so it starts from disk: a field the
// chat's own turn wrote after the registry's copy (a title, pending
// input) survives the pairing.
func TestPoolTurnsLinkKeepsMetaWrittenSinceTheRegistryCopy(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	anton := seedTeamAgent(t, owner.ID, "anton", "p1")
	chat := openChat(t, owner, anton.ID, true)
	disk, err := session.Load(globalLayout, chat)
	if err != nil {
		t.Fatal(err)
	}
	disk.Meta.Label = "renamed by its turn"
	disk.Meta.PendingInput = []string{"queued"}
	if err := session.SaveMeta(globalLayout, chat, disk.Meta); err != nil {
		t.Fatal(err)
	}
	peer := teamlink.Peer{ID: anton.ID, OwnerID: owner.ID, Handle: "anton"}
	if err := (poolTurns{}).Link(context.Background(), peer, chat, "caller"); err != nil {
		t.Fatal(err)
	}
	got, _ := session.Load(globalLayout, chat)
	if got.Meta.LinkedFromSession != "caller" || got.Meta.Label != "renamed by its turn" || len(got.Meta.PendingInput) != 1 {
		t.Fatalf("meta after Link = label %q pending %v linked %q", got.Meta.Label, got.Meta.PendingInput, got.Meta.LinkedFromSession)
	}
}
