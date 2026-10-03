package team

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func shareTestStore(t *testing.T) *Store {
	t.Helper()
	db := testDB(t)
	if err := db.AutoMigrate(&entity.AgentShare{}); err != nil {
		t.Fatal(err)
	}
	return NewStore(db)
}

func TestShareAddListRemove(t *testing.T) {
	ctx := context.Background()
	st := shareTestStore(t)
	a := &entity.AgentPersona{OwnerUserID: "owner", Handle: "helper"}
	off := &entity.AgentPersona{OwnerUserID: "owner", Handle: "sleepy", Disabled: true}
	for _, p := range []*entity.AgentPersona{a, off} {
		if err := st.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddShare(ctx, a.ID, "bob", "owner"); err != nil {
		t.Fatal(err)
	}
	// Sharing again keeps the one row.
	if err := st.AddShare(ctx, a.ID, "bob", "owner"); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if err := st.AddShare(ctx, off.ID, "bob", "owner"); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListShares(ctx, a.ID)
	if err != nil || len(rows) != 1 || rows[0].SharedWithUserID != "bob" || rows[0].CreatedBy != "owner" {
		t.Fatalf("ListShares = %+v, %v", rows, err)
	}

	// A disabled agent is not on the recipient's list.
	agents, shares, err := st.SharedWith(ctx, "bob")
	if err != nil || len(agents) != 1 || agents[0].ID != a.ID || shares[0].AgentID != a.ID {
		t.Fatalf("SharedWith = %+v, %v", agents, err)
	}
	if got, _, _ := st.SharedWith(ctx, "carol"); len(got) != 0 {
		t.Fatalf("carol sees %d shared agents", len(got))
	}

	now := time.Now()
	if err := st.MarkShareRead(ctx, a.ID, "bob", now); err != nil {
		t.Fatal(err)
	}
	if sh, err := st.ShareOf(ctx, a.ID, "bob"); err != nil || sh.LastReadAt == nil {
		t.Fatalf("ShareOf = %+v, %v", sh, err)
	}

	if err := st.RemoveShare(ctx, a.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveShare(ctx, a.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove = %v, want ErrNotFound", err)
	}
	if _, err := st.ShareOf(ctx, a.ID, "bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ShareOf after remove = %v", err)
	}
	if got, _, _ := st.SharedWith(ctx, "bob"); len(got) != 0 {
		t.Fatalf("unshared agent still listed: %+v", got)
	}
}

func TestShareDeletedAgentDropsShares(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	if err := db.AutoMigrate(&entity.AgentShare{}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(db, config.NewLayout(t.TempDir()))
	a := &entity.AgentPersona{OwnerUserID: "owner", Handle: "helper"}
	if err := svc.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddShare(ctx, a.ID, "bob", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := svc.ListShares(ctx, a.ID); len(rows) != 0 {
		t.Fatalf("shares survived the agent: %+v", rows)
	}
}

func TestShareBlock(t *testing.T) {
	if ShareBlock(entity.AgentPersona{}, false) != "" {
		t.Fatal("a plain agent must be shareable")
	}
	if ShareBlock(entity.AgentPersona{IsCaptain: true}, false) == "" {
		t.Fatal("the Captain must not be shareable")
	}
	if ShareBlock(entity.AgentPersona{Kind: "a2a-remote"}, true) == "" {
		t.Fatal("an only_me remote agent must not be shareable")
	}
}

// A recipient's chat always runs as the agent's owner, even in caller
// mode; a channel session keeps the run_as rule.
func TestSpawnIdentitySharedChat(t *testing.T) {
	agent := &entity.AgentPersona{OwnerUserID: "owner", RunAs: RunAsCaller}
	shared := session.Meta{UserID: "bob", Origin: session.OriginUI}
	if got := SpawnIdentity(shared, agent, "bob"); got != "owner" {
		t.Fatalf("shared chat runs as %q, want owner", got)
	}
	own := session.Meta{UserID: "owner", Origin: session.OriginUI}
	if got := SpawnIdentity(own, agent, "owner"); got != "owner" {
		t.Fatalf("owner chat runs as %q", got)
	}
	slack := session.Meta{UserID: "bob", Origin: session.OriginSlack}
	if got := SpawnIdentity(slack, agent, "bob"); got != "bob" {
		t.Fatalf("slack session runs as %q, want the caller", got)
	}
}
