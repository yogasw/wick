package handlers

import (
	"os"
	"testing"

	"github.com/yogasw/wick/internal/appname"
)

// TestMain points HOME at a throwaway dir for the whole package. Skill read
// paths extract the shipped skills lazily (skillsync.ReadDirs → SyncBuiltin),
// so without this a test run would mirror them into the real ~/.claude/skills
// and ~/.codex/skills.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "wick-test-home-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Unsetenv(appname.DataDirEnv)
	appname.ResetDataDirForTest()
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
