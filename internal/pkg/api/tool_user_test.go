package api

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func TestApprovedToolUser(t *testing.T) {
	users := map[string]*entity.User{
		"ok":      {ID: "ok", Name: "Ok", Email: "ok@example.com", Approved: true, Role: entity.RoleAdmin},
		"pending": {ID: "pending", Approved: false},
	}
	get := func(_ context.Context, id string) (*entity.User, error) {
		if u, ok := users[id]; ok {
			return u, nil
		}
		return nil, errors.New("record not found") // unknown or deleted
	}
	tags := func(context.Context, string) []string { return []string{"ops"} }

	u, ok := approvedToolUser(context.Background(), "ok", get, tags)
	if !ok || u.ID != "ok" || !u.IsAdmin || u.Email != "ok@example.com" || !u.HasTag("ops") {
		t.Fatalf("approved user = %+v, %v", u, ok)
	}
	for _, id := range []string{"pending", "gone"} {
		if u, ok := approvedToolUser(context.Background(), id, get, tags); ok {
			t.Errorf("%s recognised: %+v", id, u)
		}
	}
	nilGet := func(context.Context, string) (*entity.User, error) { return nil, nil }
	if _, ok := approvedToolUser(context.Background(), "x", nilGet, tags); ok {
		t.Error("nil user recognised")
	}
}
