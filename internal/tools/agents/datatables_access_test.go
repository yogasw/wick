package agents

import "testing"

// TestAllowDataTable pins the per-user isolation rule the data-table widget +
// UI depend on: a table is reachable only by its owner, users explicitly
// granted the owner:<slug> tag (slug present in the accessible set), or an
// admin with AdminSeeAll (seeAll). Ownerless tables are never visible to a
// scoped caller.
func TestAllowDataTable(t *testing.T) {
	tests := []struct {
		name  string
		acc   projectAccess
		slug  string
		owner string
		want  bool
	}{
		{
			name: "owner reaches own table via fallback",
			acc:  projectAccess{userID: "u1", projects: map[string]struct{}{}},
			slug: "tasks", owner: "u1", want: true,
		},
		{
			name: "non-owner without grant is denied",
			acc:  projectAccess{userID: "u2", projects: map[string]struct{}{}},
			slug: "tasks", owner: "u1", want: false,
		},
		{
			name: "tag-granted user reaches table (slug in accessible set)",
			acc:  projectAccess{userID: "u2", projects: map[string]struct{}{"tasks": {}}},
			slug: "tasks", owner: "u1", want: true,
		},
		{
			name: "AdminSeeAll reaches any table",
			acc:  projectAccess{seeAll: true},
			slug: "tasks", owner: "u1", want: true,
		},
		{
			name: "ownerless table is hidden from scoped caller",
			acc:  projectAccess{userID: "u2", projects: map[string]struct{}{}},
			slug: "legacy", owner: "", want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.acc.allowDataTable(tt.slug, tt.owner); got != tt.want {
				t.Fatalf("allowDataTable(%q,%q) = %v, want %v", tt.slug, tt.owner, got, tt.want)
			}
		})
	}
}
