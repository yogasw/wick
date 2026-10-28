package wickmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/promptguard"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/pkg/connector"
)

func newPromptHandlers(t *testing.T) (*handlers, *[]promptguard.Card) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	cfgs := configs.NewService(db)
	if err := cfgs.Bootstrap(context.Background(), entity.Config{Owner: "agents", Key: promptguard.KeySecurity, Type: "textarea"}); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	pg := promptguard.New(db, cfgs)
	var cards []promptguard.Card
	pg.Notify = func(c promptguard.Card) { cards = append(cards, c) }
	return newHandlers(Deps{Configs: cfgs, Prompts: pg}), &cards
}

func promptCtx(u *entity.User, session string, in map[string]string) *connector.Ctx {
	ctx := context.Background()
	if u != nil {
		ctx = login.WithUser(ctx, u, nil)
	}
	c := connector.NewCtx(ctx, "", nil, in, nil, nil, nil)
	c.SetSessionID(session)
	return c
}

// tool_set_config cannot write an operator prompt, even for an admin:
// that would skip the approval card.
func TestToolSetConfigRefusesManagedPrompt(t *testing.T) {
	h, _ := newPromptHandlers(t)
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	for _, key := range []string{promptguard.KeySecurity, promptguard.KeySystem, promptguard.KeyTeam} {
		_, err := h.toolSetConfig(promptCtx(admin, "s1", map[string]string{"key": "agents", "config_key": key, "value": "x"}))
		if !errors.Is(err, errManagedPrompt) {
			t.Errorf("%s: err = %v, want errManagedPrompt", key, err)
		}
	}
}

// prompt_set only files a proposal: admin-only, needs a session, and the
// prompt stays as it was until the card is accepted.
func TestPromptSetOnlyProposes(t *testing.T) {
	h, cards := newPromptHandlers(t)
	admin := &entity.User{ID: "u-admin", Name: "Admin A", Role: entity.RoleAdmin}
	plain := &entity.User{ID: "u-plain"}
	in := map[string]string{"key": promptguard.KeySecurity, "value": "rule", "reason": "test"}

	if _, err := h.promptSet(promptCtx(plain, "s1", in)); !errors.Is(err, errAccessDenied) {
		t.Errorf("non-admin: err = %v", err)
	}
	if _, err := h.promptSet(promptCtx(admin, "", in)); !errors.Is(err, promptguard.ErrNoSession) {
		t.Errorf("no session: err = %v", err)
	}
	out, err := h.promptSet(promptCtx(admin, "s1", in))
	if err != nil {
		t.Fatal(err)
	}
	if out.(map[string]any)["status"] != "pending_approval" || len(*cards) != 1 {
		t.Fatalf("out = %v cards = %v", out, *cards)
	}
	got, err := h.promptGet(promptCtx(plain, "", map[string]string{"key": promptguard.KeySecurity}))
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["value"] != "" || len(m["pending_proposals"].([]string)) != 1 {
		t.Fatalf("prompt_get = %v", m)
	}
}
