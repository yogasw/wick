package project

import (
	"sort"
	"testing"
)

func TestCanAccess(t *testing.T) {
	tests := []struct {
		name string
		meta Meta
		acc  Access
		want bool
	}{
		{
			name: "admin sees owned-by-other",
			meta: Meta{OwnerUserID: "other"},
			acc:  Access{UserID: "me", IsAdmin: true},
			want: true,
		},
		{
			name: "unowned untagged is shared",
			meta: Meta{},
			acc:  Access{UserID: "me"},
			want: true,
		},
		{
			name: "owner sees own",
			meta: Meta{OwnerUserID: "me"},
			acc:  Access{UserID: "me"},
			want: true,
		},
		{
			name: "non-owner cannot see other's untagged-owned",
			meta: Meta{OwnerUserID: "other"},
			acc:  Access{UserID: "me"},
			want: false,
		},
		{
			name: "tag share grants access",
			meta: Meta{OwnerUserID: "other", Tags: []string{"team-x"}},
			acc:  Access{UserID: "me", TagIDs: []string{"team-x", "team-y"}},
			want: true,
		},
		{
			name: "tagged project, user lacks tag",
			meta: Meta{Tags: []string{"team-x"}},
			acc:  Access{UserID: "me", TagIDs: []string{"team-z"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanAccess(tt.meta, tt.acc); got != tt.want {
				t.Errorf("CanAccess = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListVisibleTo(t *testing.T) {
	layout := newLayout(t)

	mk := func(id, owner string, tags ...string) {
		if _, err := Create(layout, CreateOptions{ID: id, Name: id, OwnerUserID: owner, Tags: tags}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk("shared", "")            // unowned, untagged
	mk("mine", "me")            // owned by me
	mk("theirs", "other")       // owned by someone else
	mk("teamx", "other", "tx")  // owned by other but tagged tx

	// Non-admin "me" carrying tag tx: sees shared + mine + teamx, not theirs.
	got, err := ListVisibleTo(layout, Access{UserID: "me", TagIDs: []string{"tx"}})
	if err != nil {
		t.Fatalf("ListVisibleTo: %v", err)
	}
	sort.Strings(got)
	want := []string{"mine", "shared", "teamx"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}

	// Admin sees everything.
	all, _ := ListVisibleTo(layout, Access{UserID: "me", IsAdmin: true})
	if len(all) != 4 {
		t.Fatalf("admin should see 4, got %d (%v)", len(all), all)
	}
}
