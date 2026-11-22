package admin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestManageUsersAndTags(t *testing.T) {
	ctx := context.Background()
	h := impersonateHandler(t,
		&entity.User{ID: "u-admin", Email: "boss@acme.test", Name: "Boss", Role: entity.RoleAdmin, Approved: true},
		&entity.User{ID: "u-new", Email: "dev@acme.test", Name: "Dev", Role: entity.RoleUser},
	)
	var notified []string
	h.SetOnUserApproved(func(_ context.Context, id string) { notified = append(notified, id) })

	pending, err := h.ManageListUsers(ctx, "pending", "")
	if err != nil || len(pending) != 1 || pending[0].ID != "u-new" {
		t.Fatalf("pending = %+v, %v", pending, err)
	}

	if _, err := h.ManageSetUserTags(ctx, "dev@acme.test", nil, []string{"x"}, nil, false); err == nil {
		t.Fatal("tags on an unknown tag must fail")
	}
	if _, err := h.ManageCreateTag(ctx, "Team A", "first team", false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ManageCreateTag(ctx, "Team B", "", false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ManageSetUserTags(ctx, "dev@acme.test", nil, []string{"team a"}, nil, false); !errors.Is(err, ErrUserNotApproved) {
		t.Fatalf("tags before approval: err = %v, want ErrUserNotApproved", err)
	}

	if u, err := h.ManageSetApproved(ctx, "DEV@acme.test", true); err != nil || !u.Approved {
		t.Fatalf("approve: %+v, %v", u, err)
	}
	if !reflect.DeepEqual(notified, []string{"u-new"}) {
		t.Fatalf("approval notice = %v, want [u-new]", notified)
	}
	// Approving again must not notify twice.
	if _, err := h.ManageSetApproved(ctx, "u-new", true); err != nil || len(notified) != 1 {
		t.Fatalf("re-approve notified again: %v, %v", notified, err)
	}

	got, err := h.ManageSetUserTags(ctx, "u-new", nil, []string{"Team A", "Team B"}, nil, false)
	if err != nil || !reflect.DeepEqual(got, []string{"Team A", "Team B"}) {
		t.Fatalf("add tags = %v, %v", got, err)
	}
	got, err = h.ManageSetUserTags(ctx, "u-new", nil, nil, []string{"team b"}, false)
	if err != nil || !reflect.DeepEqual(got, []string{"Team A"}) {
		t.Fatalf("remove tag = %v, %v", got, err)
	}
	got, err = h.ManageSetUserTags(ctx, "u-new", []string{"Team B"}, nil, nil, true)
	if err != nil || !reflect.DeepEqual(got, []string{"Team B"}) {
		t.Fatalf("replace tags = %v, %v", got, err)
	}

	name := "Team Bee"
	if m, err := h.ManageUpdateTag(ctx, "Team B", TagPatch{Name: &name}); err != nil || m["name"] != name {
		t.Fatalf("rename = %v, %v", m, err)
	}
	if n, err := h.ManageDeleteTag(ctx, "team bee"); err != nil || n != name {
		t.Fatalf("delete = %q, %v", n, err)
	}

	if _, err := h.ManageSetRole(ctx, "boss@acme.test", "user"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin: err = %v, want ErrLastAdmin", err)
	}
	if _, err := h.ManageSetRole(ctx, "u-new", "owner"); err == nil {
		t.Fatal("invalid role accepted")
	}
	if _, err := h.ManageSetApproved(ctx, "nobody@acme.test", true); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user: err = %v", err)
	}
}
