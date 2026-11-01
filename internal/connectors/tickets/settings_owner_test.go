package tickets

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
)

// Changing ticket settings is the project owner's call, as on the settings
// page: a member who reaches the project through a tag grant may read the
// settings but not set them.
func TestSettingsSetIsOwnerOnly(t *testing.T) {
	h, l := newTestHandlers(t)
	if _, err := project.Create(l, project.CreateOptions{ID: "acme", Name: "acme", OwnerUserID: "user-a", Tags: []string{"tag-x"}}); err != nil {
		t.Fatal(err)
	}
	as := func(uid string, input map[string]string) *connector.Ctx {
		ctx := login.WithUser(context.Background(), &entity.User{ID: uid, Role: entity.RoleUser}, []string{"tag-x"})
		c := connector.NewCtx(ctx, "self-test", nil, input, nil, nil, nil)
		c.SetCallerUserID(uid)
		return c
	}
	set := map[string]string{"project_id": "acme", "enabled": "true"}

	mustDispatch(t, h.settingsGet, as("user-b", map[string]string{"project_id": "acme"}))
	if _, err := h.settingsSet(as("user-b", set)); !errors.Is(err, team.ErrNotProjectOwner) {
		t.Fatalf("member set: err = %v, want ErrNotProjectOwner", err)
	}
	if p, _ := project.Load(l, "acme"); p.Meta.Ticket.Enabled {
		t.Fatal("a refused set must not change the project")
	}
	got := mustDispatch(t, h.settingsSet, as("user-a", set)).(settingsView)
	if !got.Enabled {
		t.Fatal("owner set not applied")
	}
}
