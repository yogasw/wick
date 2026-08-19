package claude

import (
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/skillsync"
)

// only builds an exists-func that accepts exactly the given paths, so a test
// never depends on what happens to be on the developer's machine.
func only(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	return func(p string) bool { return set[p] }
}

func TestSkillAddDirArgs(t *testing.T) {
	home := "/home/u"
	skills := filepath.Join(home, ".claude", "skills")

	// Dir exists → emit --add-dir for it.
	got := skillAddDirArgs(home, only(skills))
	if len(got) != 2 || got[0] != "--add-dir" || got[1] != skills {
		t.Fatalf("got %v, want [--add-dir %s]", got, skills)
	}

	// Dir missing → no args (don't trust a path that isn't there).
	if a := skillAddDirArgs(home, func(string) bool { return false }); a != nil {
		t.Fatalf("missing dir should yield nil, got %v", a)
	}
}

// TestSkillAddDirArgsTrustsWickDir: wick's shipped skills are deliberately not
// copied into ~/.claude/skills, so claude's own loader never sees them. The
// system prompt names them by absolute path instead, and that path is only
// readable if this dir is trusted too — without it the agent is told to read
// files its sandbox refuses to open.
func TestSkillAddDirArgsTrustsWickDir(t *testing.T) {
	wickDir := skillsync.BuiltinDir()
	if wickDir == "" {
		t.Skip("no wick skills dir resolvable in this environment")
	}
	got := skillAddDirArgs("/home/u", only(wickDir))
	if len(got) != 2 || got[1] != wickDir {
		t.Fatalf("got %v, want [--add-dir %s]", got, wickDir)
	}
}

// TestSkillAddDirArgsHonorsConfigDir: $CLAUDE_CONFIG_DIR relocates claude's
// whole config tree, skills included. Trusting the hardcoded ~/.claude/skills
// on such a machine authorises a directory the CLI does not read and leaves the
// one it does read blocked.
func TestSkillAddDirArgsHonorsConfigDir(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	relocated := filepath.Join(cfg, "skills")

	got := skillAddDirArgs("/home/u", only(relocated))
	if len(got) != 2 || got[1] != relocated {
		t.Fatalf("got %v, want [--add-dir %s]", got, relocated)
	}
	// The default location must not be trusted when the env var moved it.
	for _, a := range skillAddDirArgs("/home/u", func(string) bool { return true }) {
		if a == filepath.Join("/home/u", ".claude", "skills") {
			t.Error("trusted the default skills dir while CLAUDE_CONFIG_DIR relocated it")
		}
	}
}

// TestSkillAddDirArgsEmptyHome: a home-dir lookup failure must not cost the
// agent its shipped skills — wick's dir can still resolve via $WICK_DATA_DIR.
func TestSkillAddDirArgsEmptyHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got := skillAddDirArgs("", func(string) bool { return true })
	for _, a := range got {
		if a == filepath.Join(".claude", "skills") {
			t.Errorf("empty home produced a rootless claude skills path: %v", got)
		}
	}
	if wickDir := skillsync.BuiltinDir(); wickDir != "" {
		if len(got) != 2 || got[1] != wickDir {
			t.Errorf("got %v, want just [--add-dir %s]", got, wickDir)
		}
	}
}
