package delegation

import "testing"

func TestAllocateHandle(t *testing.T) {
	tests := []struct {
		name    string
		profile string
		taken   []string
		want    string
	}{
		{"first of a role keeps the bare key", "reviewer", nil, "reviewer"},
		{"second gets -2", "reviewer", []string{"reviewer"}, "reviewer-2"},
		{"gap is reused rather than climbing", "reviewer", []string{"reviewer", "reviewer-3"}, "reviewer-2"},
		{"leader name is reserved", "main", nil, "main-2"},
		{"uppercase folds down", "Code-Reviewer", nil, "code-reviewer"},
		{"spaces become dashes", "code reviewer", nil, "code-reviewer"},
		{"empty key still yields an address", "", nil, "agent"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllocateHandle(tc.profile, tc.taken); got != tc.want {
				t.Fatalf("AllocateHandle(%q, %v) = %q, want %q", tc.profile, tc.taken, got, tc.want)
			}
		})
	}
}
