package persona

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	// Named shared-cache DB: plain ":memory:" gives every pooled
	// connection its own empty database.
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&entity.AgentPersona{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestStoreCaptainIsUniquePerOwner(t *testing.T) {
	ctx := context.Background()
	st := NewStore(testDB(t))
	a := &entity.AgentPersona{OwnerUserID: "u1", Handle: "captain", IsCaptain: true}
	b := &entity.AgentPersona{OwnerUserID: "u1", Handle: "helper"}
	other := &entity.AgentPersona{OwnerUserID: "u2", Handle: "captain", IsCaptain: true}
	for _, p := range []*entity.AgentPersona{a, b, other} {
		if err := st.Create(ctx, p); err != nil {
			t.Fatalf("create %s: %v", p.Handle, err)
		}
	}
	b.IsCaptain = true
	if err := st.Update(ctx, b); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows, err := st.List(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	captains := 0
	for _, r := range rows {
		if r.IsCaptain {
			captains++
			if r.ID != b.ID {
				t.Errorf("captain = %s, want %s", r.Handle, b.Handle)
			}
		}
	}
	if captains != 1 {
		t.Fatalf("captains = %d, want 1", captains)
	}
	if rows[0].ID != b.ID {
		t.Errorf("List must put the Captain first, got %s", rows[0].Handle)
	}
	// Another owner's Captain is untouched.
	got, err := st.Get(ctx, other.ID)
	if err != nil || !got.IsCaptain {
		t.Fatalf("other owner's captain lost: %+v %v", got, err)
	}
}

func TestStoreHandleRules(t *testing.T) {
	ctx := context.Background()
	st := NewStore(testDB(t))
	if err := st.Create(ctx, &entity.AgentPersona{OwnerUserID: "u1", Handle: "@Writer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetByHandle(ctx, "u1", "writer"); err != nil {
		t.Fatalf("handle not normalized on save: %v", err)
	}
	if err := st.Create(ctx, &entity.AgentPersona{OwnerUserID: "u1", Handle: "writer"}); !errors.Is(err, ErrHandleTaken) {
		t.Fatalf("dup handle err = %v, want ErrHandleTaken", err)
	}
	if err := st.Create(ctx, &entity.AgentPersona{OwnerUserID: "u2", Handle: "writer"}); err != nil {
		t.Fatalf("same handle for another owner must be allowed: %v", err)
	}
	if err := st.Create(ctx, &entity.AgentPersona{OwnerUserID: "u1", Handle: "all"}); err == nil {
		t.Fatal("reserved handle accepted")
	}
	if _, err := st.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
	if err := st.Delete(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing = %v", err)
	}
}

func TestAgentOfSessionWalksParents(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	ctx := context.Background()
	mk := func(id, parent, agent string) {
		t.Helper()
		if _, err := session.Create(ctx, layout, session.CreateOptions{ID: id, ParentSessionID: parent, AgentID: agent}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("s-root", "", "agent-1")
	mk("s-child", "s-root", "")
	mk("s-grand", "s-child", "")
	mk("s-plain", "", "")
	if got := AgentOfSession(layout, "s-grand"); got != "agent-1" {
		t.Errorf("grandchild = %q, want agent-1", got)
	}
	if got := AgentOfSession(layout, "s-plain"); got != "" {
		t.Errorf("plain = %q, want empty", got)
	}
	if got := AgentOfSession(layout, "missing"); got != "" {
		t.Errorf("missing = %q, want empty", got)
	}
}

func TestScopeForSession(t *testing.T) {
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	svc := NewService(testDB(t), layout)
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	p := &entity.AgentPersona{OwnerUserID: "u1", Handle: "worker",
		AllowedConnectors: EncodeGrants([]ConnectorGrant{{ConnectorID: "c1", Level: LevelRead}})}
	if err := svc.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	for _, s := range []session.CreateOptions{
		{ID: "s-agent", AgentID: p.ID},
		{ID: "s-plain"},
		{ID: "s-gone", AgentID: "deleted-agent"},
	} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}
	if svc.ScopeForSession(ctx, "s-plain") != nil {
		t.Error("plain session must be unscoped")
	}
	if svc.ScopeForSession(ctx, "s-gone") != nil {
		t.Error("session of a deleted agent must be unscoped")
	}
	sc := svc.ScopeForSession(ctx, "s-agent")
	if sc == nil || !sc.AllowConnector("c1") || sc.AllowConnector("c2") {
		t.Fatalf("agent scope wrong: %v", sc)
	}

	// Update invalidates the cache; disabling switches to deny-all.
	p.Disabled = true
	if err := svc.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if sc := svc.ScopeForSession(ctx, "s-agent"); sc == nil || sc.AllowConnector("c1") {
		t.Fatal("disabled agent must resolve to deny-all, not nil")
	}
}
