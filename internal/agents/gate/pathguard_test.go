package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardFixture builds base/projects/{own,other}, base/sessions/{mine,theirs}
// and a symlink inside own pointing into other.
func guardFixture(t *testing.T) (PathGuardSpec, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"projects/own/files", "projects/other/files", "projects/shared/files", "sessions/mine", "sessions/theirs", "home"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(base, "projects/other/files"), filepath.Join(base, "projects/own/files/link")); err != nil {
		t.Fatal(err)
	}
	spec := PathGuardSpec{
		Guarded: []string{filepath.Join(base, "projects"), filepath.Join(base, "sessions")},
		Allow: []string{
			filepath.Join(base, "projects/own"), filepath.Join(base, "sessions/mine"),
			filepath.Join(base, "projects/shared/files"),
		},
		Home: filepath.Join(base, "home"),
	}
	return spec, base
}

func TestPathGuard(t *testing.T) {
	spec, base := guardFixture(t)
	cwd := filepath.Join(base, "projects/own/files")
	cases := []struct {
		name    string
		input   map[string]any
		blocked bool
	}{
		{"own file", map[string]any{"file_path": filepath.Join(cwd, "a.txt")}, false},
		{"own relative", map[string]any{"file_path": "notes/a.md"}, false},
		{"other project", map[string]any{"file_path": filepath.Join(base, "projects/other/files/CLAUDE.md")}, true},
		{"relative escape", map[string]any{"file_path": "../../other/files/x"}, true},
		{"symlink into other", map[string]any{"file_path": "link/secret.txt"}, true},
		{"projects root listing", map[string]any{"path": filepath.Join(base, "projects")}, true},
		{"glob into others", map[string]any{"pattern": filepath.Join(base, "projects") + "/*/files/**"}, true},
		{"allowed exception", map[string]any{"file_path": filepath.Join(base, "projects/shared/files/x")}, false},
		{"own session", map[string]any{"file_path": filepath.Join(base, "sessions/mine/deploy.log")}, false},
		{"other session", map[string]any{"file_path": filepath.Join(base, "sessions/theirs/deploy.sh")}, true},
		{"outside guarded roots", map[string]any{"file_path": "/etc/hostname"}, false},
		{"bash cat other", map[string]any{"command": "cat ../../other/files/data.txt | head"}, true},
		{"bash ls glob", map[string]any{"command": "ls " + filepath.Join(base, "projects") + "/*"}, true},
		{"bash own work", map[string]any{"command": "git -C ./repo status && cat notes/a.md"}, false},
		{"bash url ignored", map[string]any{"command": "curl -s https://example.com/a/b"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckPathGuard(spec, PathGuardInput{CWD: cwd, ToolInput: tc.input})
			if (got != "") != tc.blocked {
				t.Fatalf("blocked=%q, want blocked=%v", got, tc.blocked)
			}
		})
	}
}

// A tilde path is expanded against the spec's home.
func TestPathGuardTilde(t *testing.T) {
	spec, base := guardFixture(t)
	spec.Guarded = append(spec.Guarded, filepath.Join(base, "home/.secret"))
	got := CheckPathGuard(spec, PathGuardInput{CWD: base, ToolInput: map[string]any{"command": "cat ~/.secret/x"}})
	if got == "" {
		t.Fatal("~ path not expanded")
	}
}

// The settings file names the gate binary in path guard mode with the
// spec next to it.
func TestWritePathGuard(t *testing.T) {
	dir := t.TempDir()
	settings, err := WritePathGuard(dir, "/opt/x/app-gate", PathGuardSpec{Guarded: []string{"/a"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	want := `"/opt/x/app-gate --path-guard ` + filepath.Join(dir, "pathguard-spec.json") + `"`
	if !strings.Contains(string(data), want) {
		t.Fatalf("settings = %s, want command %s", data, want)
	}
	if spec, err := LoadPathGuardSpec(filepath.Join(dir, "pathguard-spec.json")); err != nil || len(spec.Guarded) != 1 {
		t.Fatalf("spec round trip: %+v %v", spec, err)
	}
}
