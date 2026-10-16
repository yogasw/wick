package login

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func newHiddenSvc(t *testing.T) (*Service, string) {
	t.Helper()
	svc := NewService(newLoginSQLite(t), "")
	u, err := svc.UpsertUser(context.Background(), "hide-test@example.test", "Hide Test", "")
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	return svc, u.ID
}

func TestSetConnectorHiddenIdempotent(t *testing.T) {
	svc, uid := newHiddenSvc(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		got, err := svc.SetConnectorHidden(ctx, uid, "c1", true, nil)
		if err != nil {
			t.Fatalf("hide #%d: %v", i, err)
		}
		if !reflect.DeepEqual(got, []string{"c1"}) {
			t.Fatalf("hide #%d list = %v, want [c1]", i, got)
		}
	}
	got, err := svc.SetConnectorHidden(ctx, uid, "c2", true, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"c1", "c2"}) {
		t.Fatalf("hide c2 = %v, %v", got, err)
	}
	got, err = svc.SetConnectorHidden(ctx, uid, "c1", false, nil)
	if err != nil || !reflect.DeepEqual(got, []string{"c2"}) {
		t.Fatalf("unhide c1 = %v, %v", got, err)
	}
	// Unhiding an id never hidden (or long deleted) is a no-op, not an error.
	got, err = svc.SetConnectorHidden(ctx, uid, "gone", false, func(string) bool { return false })
	if err != nil || !reflect.DeepEqual(got, []string{"c2"}) {
		t.Fatalf("unhide unknown = %v, %v", got, err)
	}
	u, _ := svc.GetUserByID(ctx, uid)
	if !reflect.DeepEqual(u.Metadata.HiddenConnectors, []string{"c2"}) {
		t.Errorf("stored = %v, want [c2]", u.Metadata.HiddenConnectors)
	}
}

// The setter rewrites only its own field: preferences saved by other
// setters survive a hide.
func TestSetConnectorHiddenKeepsOtherMetadata(t *testing.T) {
	svc, uid := newHiddenSvc(t)
	ctx := context.Background()
	if err := svc.SetSidebarWidth(ctx, uid, "team", 320); err != nil {
		t.Fatalf("sidebar: %v", err)
	}
	if err := svc.SetTheme(ctx, uid, "dark"); err != nil {
		t.Fatalf("theme: %v", err)
	}
	before, _ := svc.GetUserByID(ctx, uid)
	if _, err := svc.SetConnectorHidden(ctx, uid, "c1", true, nil); err != nil {
		t.Fatalf("hide: %v", err)
	}
	after, _ := svc.GetUserByID(ctx, uid)
	if after.Metadata.Sidebar != before.Metadata.Sidebar || after.Metadata.Theme != before.Metadata.Theme {
		t.Errorf("hide touched other prefs: before %+v after %+v", before.Metadata, after.Metadata)
	}
}

func TestSetConnectorHiddenCapAndPrune(t *testing.T) {
	svc, uid := newHiddenSvc(t)
	ctx := context.Background()
	for i := 0; i < entity.MaxHiddenConnectors; i++ {
		if _, err := svc.SetConnectorHidden(ctx, uid, fmt.Sprintf("c%03d", i), true, nil); err != nil {
			t.Fatalf("hide %d: %v", i, err)
		}
	}
	allExist := func(string) bool { return true }
	if _, err := svc.SetConnectorHidden(ctx, uid, "extra", true, allExist); !errors.Is(err, ErrHiddenLimit) {
		t.Fatalf("over cap err = %v, want ErrHiddenLimit", err)
	}
	// Re-hiding an id already on a full list is still fine.
	if _, err := svc.SetConnectorHidden(ctx, uid, "c000", true, allExist); err != nil {
		t.Fatalf("re-hide at cap: %v", err)
	}
	// Ids whose instance is gone are pruned to make room.
	gone := map[string]bool{"c001": true, "c002": true}
	got, err := svc.SetConnectorHidden(ctx, uid, "extra", true, func(id string) bool { return !gone[id] })
	if err != nil {
		t.Fatalf("hide after prune: %v", err)
	}
	if len(got) != entity.MaxHiddenConnectors-1 {
		t.Errorf("len = %d, want %d", len(got), entity.MaxHiddenConnectors-1)
	}
	set := entity.UserMetadata{HiddenConnectors: got}.HiddenConnectorSet()
	if set["c001"] || set["c002"] || !set["extra"] || !set["c003"] {
		t.Errorf("prune wrong: c001=%v c002=%v extra=%v c003=%v", set["c001"], set["c002"], set["extra"], set["c003"])
	}
}

func TestSetConnectorHiddenUnknownUser(t *testing.T) {
	svc, _ := newHiddenSvc(t)
	if _, err := svc.SetConnectorHidden(context.Background(), "no-such-user", "c1", true, nil); err == nil {
		t.Fatal("hide for a missing user succeeded")
	}
}
