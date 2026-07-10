package provider

import "testing"

func TestValidInstanceName(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty", "", true},
		{"space", "abc a", true},
		{"slash", "abc/a", true},
		{"dot", "abc.a", true},
		{"hyphen rejected", "abc-a", true},
		{"underscore ok", "abc_a", false},
		{"alnum ok", "Abc123", false},
		{"bare type ok", "claude", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidInstanceName(tc.in)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidInstanceName(%q) = nil, want error", tc.in)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidInstanceName(%q) = %v, want nil", tc.in, err)
			}
		})
	}
}

// isolateConfig points userconfig at a throwaway home dir + unique app
// name so Save/Find/Rename hit a clean store and never touch the real
// user config.
func isolateConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows reads this
	prev := AppName()
	SetAppName("wick-rename-test")
	invalidateInstanceCache()
	// Save spawns a fire-and-forget rescan goroutine that persists a
	// probe into `home`. It outlives the test body, so without draining
	// it here it races t.TempDir()'s RemoveAll and leaves config.json
	// behind ("directory not empty"). Cleanups run LIFO and TempDir's
	// removal is registered first (line above), so this drain runs
	// before the dir is torn down.
	t.Cleanup(func() {
		waitBackgroundRescans()
		SetAppName(prev)
		invalidateInstanceCache()
	})
}

// saveSeed persists an instance and drains Save's fire-and-forget probe
// goroutine before returning. That goroutine reloads + atomically rewrites
// config.json; leaving it in flight lets it race the next Save/Rename/Switch
// (a stale read resurrecting an old name under -race, or a concurrent atomic
// rename failing with EACCES on Windows). Draining serialises every config
// write so the ordering-sensitive assertions below are deterministic.
func saveSeed(t *testing.T, ins Instance) {
	t.Helper()
	if err := Save(ins); err != nil {
		t.Fatalf("seed save %s/%s: %v", ins.Type, ins.Name, err)
	}
	waitBackgroundRescans()
}

func TestRename(t *testing.T) {
	isolateConfig(t)

	saveSeed(t, Instance{Type: TypeClaude, Name: "abc"})

	t.Run("rejects invalid new name", func(t *testing.T) {
		if err := Rename(TypeClaude, "abc", "abc a"); err == nil {
			t.Fatal("want error for name with space")
		}
	})

	t.Run("renames and old key gone", func(t *testing.T) {
		if err := Rename(TypeClaude, "abc", "abc_b"); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if _, err := Find(TypeClaude, "abc_b"); err != nil {
			t.Fatalf("new name not found after rename: %v", err)
		}
		// Old custom name should no longer resolve to a persisted instance.
		// Find auto-falls-back only for the canonical default (name==type),
		// never for a deleted custom name.
		if _, err := Find(TypeClaude, "abc"); err == nil {
			t.Fatal("old name still resolves after rename")
		}
	})

	t.Run("rejects collision with existing instance", func(t *testing.T) {
		saveSeed(t, Instance{Type: TypeClaude, Name: "other"})
		if err := Rename(TypeClaude, "other", "abc_b"); err == nil {
			t.Fatal("want collision error renaming onto existing abc_b")
		}
	})
}
