package appname

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setDataDir points $WICK_DATA_DIR at dir for one test and drops the
// memoized value on both sides of the test, so neither this test nor
// the next one reads a cached path from a sibling.
func setDataDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv(DataDirEnv, dir)
	ResetDataDirForTest()
	t.Cleanup(ResetDataDirForTest)
}

func TestDataDirDefaultsUnderHome(t *testing.T) {
	t.Setenv(DataDirEnv, "")
	ResetDataDirForTest()
	t.Cleanup(ResetDataDirForTest)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}
	want := filepath.Join(home, "."+Resolve())
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

func TestDataDirEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	setDataDir(t, dir)

	if got := DataDir(); got != dir {
		t.Errorf("DataDir() = %q, want %q", got, dir)
	}
}

// The whole point of the override is that one env moves every artefact.
// A sub-tree that kept deriving its own path from the home dir would
// split the data across two locations, which is the bug this guards.
func TestSubTreesFollowOverride(t *testing.T) {
	dir := t.TempDir()
	setDataDir(t, dir)

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"agents", AgentsDir(), filepath.Join(dir, "agents")},
		{"skills", SkillsDir(), filepath.Join(dir, "skills")},
		{"logs", LogsDir(), filepath.Join(dir, "logs")},
	} {
		if tc.got != tc.want {
			t.Errorf("%sDir() = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestDataDirOverrideEmptyWithoutEnv(t *testing.T) {
	t.Setenv(DataDirEnv, "")
	ResetDataDirForTest()
	t.Cleanup(ResetDataDirForTest)

	if got := DataDirOverride(); got != "" {
		t.Errorf("DataDirOverride() = %q, want empty", got)
	}
}

// Whitespace-only is treated as unset rather than as a path: a blank
// value in a .env or a shell export is far more likely to be a mistake
// than a deliberate request to write into the cwd.
func TestDataDirIgnoresBlankEnv(t *testing.T) {
	setDataDir(t, "   ")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}
	want := filepath.Join(home, "."+Resolve())
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %q, want default %q", got, want)
	}
}

func TestDataDirExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}
	setDataDir(t, "~/wick-data-test")

	want := filepath.Join(home, "wick-data-test")
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

// A relative override is pinned to an absolute path at resolve time.
// wick chdirs to the project root on some entry points (MCP stdio), so
// a path left relative would silently move mid-process.
func TestDataDirMakesRelativeAbsolute(t *testing.T) {
	setDataDir(t, "wick-data-rel")

	got := DataDir()
	if !filepath.IsAbs(got) {
		t.Fatalf("DataDir() = %q, want an absolute path", got)
	}
	if base := filepath.Base(got); base != "wick-data-rel" {
		t.Errorf("DataDir() base = %q, want %q", base, "wick-data-rel")
	}
}

func TestDataDirAcceptsBackslashTildeOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("backslash tilde form is Windows-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}
	setDataDir(t, `~\wick-data-test`)

	want := filepath.Join(home, "wick-data-test")
	if got := DataDir(); got != want {
		t.Errorf("DataDir() = %q, want %q", got, want)
	}
}

// DataDir is memoized: a path that changed mid-process would split one
// run's writes across two trees, so a later env change must not take
// effect until something explicitly resets it.
func TestDataDirIsCachedForProcessLifetime(t *testing.T) {
	first := t.TempDir()
	setDataDir(t, first)

	got := DataDir()
	if got != first {
		t.Fatalf("DataDir() = %q, want %q", got, first)
	}

	second := t.TempDir()
	t.Setenv(DataDirEnv, second)
	if got := DataDir(); got != first {
		t.Errorf("DataDir() = %q after env change, want cached %q", got, first)
	}
	// DataDirOverride reads the env live by design — callers use it as a
	// predicate, not as the cached root.
	if got := DataDirOverride(); !strings.EqualFold(got, second) {
		t.Errorf("DataDirOverride() = %q, want %q", got, second)
	}
}
