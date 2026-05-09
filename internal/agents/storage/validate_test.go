package storage

import "testing"

func TestValidateNames(t *testing.T) {
	cases := []struct {
		name      string
		workspace bool
		session   bool
		preset    bool
	}{
		{"abc", true, true, true},
		{"abc-1_2", true, true, true},
		{"1715167891.234567", false, true, false},
		{"", false, false, false},
		{"../etc", false, false, false},
		{".hidden", false, false, false},
		{"a/b", false, false, false},
	}
	for _, tc := range cases {
		if got := ValidateWorkspaceName(tc.name) == nil; got != tc.workspace {
			t.Errorf("workspace %q: got valid=%v want %v", tc.name, got, tc.workspace)
		}
		if got := ValidateSessionID(tc.name) == nil; got != tc.session {
			t.Errorf("session %q: got valid=%v want %v", tc.name, got, tc.session)
		}
		if got := ValidatePresetName(tc.name) == nil; got != tc.preset {
			t.Errorf("preset %q: got valid=%v want %v", tc.name, got, tc.preset)
		}
	}
}
