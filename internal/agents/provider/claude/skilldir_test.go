package claude

import (
	"path/filepath"
	"testing"
)

func TestSkillAddDirArgs(t *testing.T) {
	home := "/home/u"
	skills := filepath.Join(home, ".claude", "skills")

	// Dir exists → emit --add-dir for it.
	got := skillAddDirArgs(home, func(p string) bool { return p == skills })
	if len(got) != 2 || got[0] != "--add-dir" || got[1] != skills {
		t.Fatalf("got %v, want [--add-dir %s]", got, skills)
	}

	// Dir missing → no args (don't trust a path that isn't there).
	if a := skillAddDirArgs(home, func(string) bool { return false }); a != nil {
		t.Fatalf("missing dir should yield nil, got %v", a)
	}

	// Empty home → nil.
	if a := skillAddDirArgs("", func(string) bool { return true }); a != nil {
		t.Fatalf("empty home should yield nil, got %v", a)
	}
}
