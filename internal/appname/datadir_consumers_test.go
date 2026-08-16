package appname_test

// The unit tests in datadir_test.go prove the root resolves correctly.
// These prove the thing that actually matters to an operator: every
// consumer that used to build `~/.<app>/...` by hand now derives its
// path from that root, so one env var moves the whole tree instead of
// half of it. They live in an _test package because the consumers
// import appname, and testing them from inside it would cycle.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentctl"
	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/connectors/plugin"
	"github.com/yogasw/wick/internal/userconfig"
)

func setDataDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(appname.DataDirEnv, dir)
	appname.ResetDataDirForTest()
	t.Cleanup(appname.ResetDataDirForTest)
}

func assertUnder(t *testing.T, root, got, label string) {
	t.Helper()
	if !strings.HasPrefix(got, root+string(filepath.Separator)) && got != root {
		t.Errorf("%s = %q, want it under %q", label, got, root)
	}
}

func TestConsumersFollowDataDirOverride(t *testing.T) {
	root := t.TempDir()
	setDataDir(t, root)

	// userconfig.Dir must ignore the app name it is handed: the override
	// names one concrete tree, not a parent to nest per-app dirs under.
	// Passing a name here is what a real caller does (appname.Resolve()).
	dir, err := userconfig.Dir("some-other-app")
	if err != nil {
		t.Fatalf("userconfig.Dir: %v", err)
	}
	if dir != root {
		t.Errorf("userconfig.Dir(name) = %q, want %q (override must outrank the name)", dir, root)
	}

	agentsBase := agentsconfig.ResolveBaseDir(agentsconfig.StorageConfig{})
	if want := filepath.Join(root, "agents"); agentsBase != want {
		t.Errorf("agents base dir = %q, want %q", agentsBase, want)
	}

	// The sockets and the gate dir are the paths two processes must agree
	// on. gate takes an app name like userconfig.Dir does, so it gets the
	// same "override outranks the name" check.
	assertUnder(t, root, agentctl.SocketPath(), "agentctl.SocketPath()")
	assertUnder(t, root, gate.SharedSocketPath("some-other-app"), "gate.SharedSocketPath()")
	assertUnder(t, root, gate.SharedSpecPath("some-other-app"), "gate.SharedSpecPath()")
	assertUnder(t, root, gate.SharedCommandsPath("some-other-app"), "gate.SharedCommandsPath()")

	// Plugins are dispensed over a socket pinned inside the tree, so they
	// have to move with it too.
	assertUnder(t, root, plugin.DefaultDir(), "plugin.DefaultDir()")
	assertUnder(t, root, plugin.RunDir(), "plugin.RunDir()")
}

// The agents Layout derives every session/project/workflow path from the
// base dir, so checking the base plus one leaf is enough to prove the
// whole sub-tree relocated.
func TestAgentsLayoutLeavesFollowOverride(t *testing.T) {
	root := t.TempDir()
	setDataDir(t, root)

	l := agentsconfig.NewLayout(agentsconfig.ResolveBaseDir(agentsconfig.StorageConfig{}))
	agents := filepath.Join(root, "agents")

	for _, tc := range []struct {
		label string
		got   string
		want  string
	}{
		{"SessionsDir", l.SessionsDir(), filepath.Join(agents, "sessions")},
		{"ProjectsDir", l.ProjectsDir(), filepath.Join(agents, "projects")},
		{"PresetsDir", l.PresetsDir(), filepath.Join(agents, "presets")},
		{"WorkflowsDir", l.WorkflowsDir(), filepath.Join(agents, "workflows")},
		{"SessionConversation", l.SessionConversation("s1"), filepath.Join(agents, "sessions", "s1", "conversation.jsonl")},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.label, tc.got, tc.want)
		}
	}
}

// The DB is the one artefact with a competing rule: project mode puts
// wick.db next to the binary when a wick.yml sits there. The override
// has to win, or an operator who relocated the tree would end up with
// the DB in one place and everything else in another.
func TestResolveDBPathFollowsOverride(t *testing.T) {
	root := t.TempDir()
	setDataDir(t, root)
	t.Setenv("DATABASE_URL", "")

	userconfig.ResolveDBPath("some-other-app", "")

	want := filepath.Join(root, "wick.db")
	if got := os.Getenv("DATABASE_URL"); got != want {
		t.Errorf("DATABASE_URL = %q, want %q", got, want)
	}
}

// Explicit settings still outrank the env: DATABASE_URL is the operator
// naming one exact file, and config.json's database_path is a stored
// version of the same intent. Neither should be silently relocated.
func TestResolveDBPathKeepsExplicitOverrides(t *testing.T) {
	t.Run("DATABASE_URL wins", func(t *testing.T) {
		setDataDir(t, t.TempDir())
		t.Setenv("DATABASE_URL", "explicit.db")

		userconfig.ResolveDBPath("some-other-app", "")

		if got := os.Getenv("DATABASE_URL"); got != "explicit.db" {
			t.Errorf("DATABASE_URL = %q, want it left untouched", got)
		}
	})

	t.Run("config database_path wins", func(t *testing.T) {
		setDataDir(t, t.TempDir())
		t.Setenv("DATABASE_URL", "")

		userconfig.ResolveDBPath("some-other-app", "custom.db")

		if got := os.Getenv("DATABASE_URL"); got != "custom.db" {
			t.Errorf("DATABASE_URL = %q, want %q", got, "custom.db")
		}
	})
}

// Without the env nothing changes shape: paths stay under ~/.<app>/ and
// stay consistent with each other. This is the regression guard for the
// default install, which is what almost every user runs.
func TestConsumersShareOneTreeWithoutOverride(t *testing.T) {
	t.Setenv(appname.DataDirEnv, "")
	appname.ResetDataDirForTest()
	t.Cleanup(appname.ResetDataDirForTest)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}
	root := filepath.Join(home, "."+appname.Resolve())

	assertUnder(t, root, agentsconfig.ResolveBaseDir(agentsconfig.StorageConfig{}), "agents base dir")
	assertUnder(t, root, agentctl.SocketPath(), "agentctl.SocketPath()")
	assertUnder(t, root, plugin.DefaultDir(), "plugin.DefaultDir()")

	dir, err := userconfig.Dir(appname.Resolve())
	if err != nil {
		t.Fatalf("userconfig.Dir: %v", err)
	}
	if dir != root {
		t.Errorf("userconfig.Dir = %q, want %q", dir, root)
	}
}
