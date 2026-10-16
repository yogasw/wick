package login

import (
	"context"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// Every field of a filter is worth keeping on its own: one with only people
// ticked, or only the untracked rail on, must be saved and not mistaken for
// the empty filter that removes the entry.
func TestSetTicketFilterKeepsSingleFieldFilters(t *testing.T) {
	for name, f := range map[string]entity.TicketFilter{
		"assignees only": {Assignees: []string{"me"}},
		"untracked only": {ShowUntracked: true},
	} {
		t.Run(name, func(t *testing.T) {
			db := newLoginSQLite(t)
			u := &entity.User{ID: "u1", Email: "u1@example.com", Name: "U1", Approved: true, Role: entity.RoleUser}
			if err := db.Create(u).Error; err != nil {
				t.Fatalf("create user: %v", err)
			}
			s := &Service{repo: newRepo(db)}
			if err := s.SetTicketFilter(context.Background(), "u1", "p1", f); err != nil {
				t.Fatalf("SetTicketFilter: %v", err)
			}
			got, err := s.repo.GetUserByID(context.Background(), "u1")
			if err != nil {
				t.Fatal(err)
			}
			saved, ok := got.Metadata.TicketFilters["p1"]
			if !ok || !reflect.DeepEqual(saved, f) {
				t.Fatalf("saved = %+v (present=%v), want %+v", saved, ok, f)
			}
			// The zero filter still clears the entry.
			if err := s.SetTicketFilter(context.Background(), "u1", "p1", entity.TicketFilter{}); err != nil {
				t.Fatal(err)
			}
			got, _ = s.repo.GetUserByID(context.Background(), "u1")
			if _, ok := got.Metadata.TicketFilters["p1"]; ok {
				t.Fatal("empty filter did not remove the entry")
			}
		})
	}
}
