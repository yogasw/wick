package subagents

import (
	"context"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// A session in a ticket room drives only delegations under itself: one
// hanging off another chat of the same person is "not found", and
// outside a room nothing changes.
func TestContinueInTicketRoomStaysUnderTheCaller(t *testing.T) {
	ctx := context.Background()
	l := agentconfig.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	mk := func(id string, edit func(m *session.Meta)) {
		t.Helper()
		if _, err := session.Create(ctx, l, session.CreateOptions{ID: id, UserID: "alice"}); err != nil {
			t.Fatal(err)
		}
		if edit != nil {
			s, _ := session.Load(l, id)
			edit(&s.Meta)
			if err := session.SaveMeta(l, id, s.Meta); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("room", func(m *session.Meta) { m.TicketRoom = &session.TicketRoom{TicketID: "T-1"} })
	mk("backing", func(m *session.Meta) { m.GroupSessionID = "room" })
	mk("sub", func(m *session.Meta) { m.ParentSessionID = "backing" })
	mk("chat", nil)

	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&entity.AgentDelegation{}); err != nil {
		t.Fatal(err)
	}
	repo := delegation.NewRepo(db)
	for _, d := range []entity.AgentDelegation{
		{ID: "d-room", ParentSessionID: "sub", ChildSessionID: "c1", TriggeredBy: "alice"},
		{ID: "d-chat", ParentSessionID: "chat", ChildSessionID: "c2", TriggeredBy: "alice"},
	} {
		d := d
		if err := repo.Create(ctx, &d); err != nil {
			t.Fatal(err)
		}
	}
	deps := Deps{Service: func() *delegation.Service { return &delegation.Service{Repo: repo} }, Layout: l}

	if err := deps.roomMayDrive(ctx, "backing", "d-room"); err != nil {
		t.Fatalf("own tree refused: %v", err)
	}
	if err := deps.roomMayDrive(ctx, "backing", "d-chat"); err == nil {
		t.Fatal("a room's backing continued a delegation of another chat")
	}
	if err := deps.roomMayDrive(ctx, "sub", "d-chat"); err == nil {
		t.Fatal("a sub-agent in the room continued a delegation of another chat")
	}
	if err := deps.roomMayDrive(ctx, "chat", "d-chat"); err != nil {
		t.Fatalf("outside a room: %v", err)
	}
}
