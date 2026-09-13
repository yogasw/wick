package view

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func admin() *entity.User  { return &entity.User{ID: "a", Approved: true, Role: entity.RoleAdmin} }
func member() *entity.User { return &entity.User{ID: "u", Approved: true, Role: entity.RoleUser} }

// Exactly one row is marked active, and it is the page we are on — the
// sidebar promotes that row above the collapsed group, so getting this
// wrong means either two highlighted rows or none.
func TestAgentsMoreItemsMarksTheActivePage(t *testing.T) {
	items := AgentsMoreItems(AgentsLayoutVM{Base: "/tools/agents", ActivePage: "resources"}, admin())

	var active []string
	for _, it := range items {
		if it.Active {
			active = append(active, it.Label)
		}
	}
	if len(active) != 1 || active[0] != "Resources" {
		t.Fatalf("active rows = %v, want exactly [Resources]", active)
	}
}

// A page outside the group leaves every row inactive: nothing is
// promoted, and "More" renders as the single closed row it used to be.
func TestAgentsMoreItemsPromotesNothingForAnOutsidePage(t *testing.T) {
	items := AgentsMoreItems(AgentsLayoutVM{Base: "/b", ActivePage: "workflows"}, admin())
	for _, it := range items {
		if it.Active {
			t.Fatalf("%s marked active for a page outside the group", it.Label)
		}
	}
}

// Visibility is per user, and the promotion must not smuggle a row past
// it: Resources is admin-only, Providers follows the manage grant.
func TestAgentsMoreItemsRespectsVisibility(t *testing.T) {
	has := func(items []AgentsNavItem, label string) bool {
		for _, it := range items {
			if it.Label == label {
				return true
			}
		}
		return false
	}

	forMember := AgentsMoreItems(AgentsLayoutVM{Base: "/b"}, member())
	if has(forMember, "Resources") {
		t.Error("Resources shown to a non-admin — its API is admin-gated")
	}
	if has(forMember, "Providers") {
		t.Error("Providers shown without a manage grant")
	}

	withProviders := AgentsMoreItems(AgentsLayoutVM{Base: "/b", ProvidersVisible: true}, member())
	if !has(withProviders, "Providers") {
		t.Error("Providers hidden from someone who manages one")
	}

	signedOut := AgentsMoreItems(AgentsLayoutVM{Base: "/b"}, nil)
	if has(signedOut, "Scheduled") || has(signedOut, "Channels") {
		t.Error("signed-in-only rows shown to a nil user")
	}
}

// Hrefs are built from the layout's base, or every link 404s on an
// install mounted under a prefix.
func TestAgentsMoreItemsUsesTheBase(t *testing.T) {
	items := AgentsMoreItems(AgentsLayoutVM{Base: "/tools/agents"}, admin())
	for _, it := range items {
		if len(it.Href) < len("/tools/agents/") || it.Href[:len("/tools/agents/")] != "/tools/agents/" {
			t.Fatalf("href = %q, want it under the base", it.Href)
		}
	}
}
