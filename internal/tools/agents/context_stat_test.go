package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatTracePath(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "shots"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "shots", "a.png"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, path, status, rel string
	}{
		{"absolute inside", filepath.Join(cwd, "shots", "a.png"), "present", "shots/a.png"},
		{"relative inside", "shots/a.png", "present", "shots/a.png"},
		{"deleted inside", filepath.Join(cwd, "shots", "gone.png"), "missing", ""},
		// Outside the session folder: never say whether it exists.
		{"absolute outside, exists", filepath.Join(outside, "secret.txt"), "unknown", ""},
		{"absolute outside, missing", filepath.Join(outside, "nope.txt"), "unknown", ""},
		{"traversal", "../" + filepath.Base(outside) + "/secret.txt", "unknown", ""},
		{"directory", filepath.Join(cwd, "shots"), "unknown", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := statTracePath(cwd, tc.path)
			if got.Status != tc.status || got.Rel != tc.rel {
				t.Fatalf("statTracePath(%q) = %+v, want status %q rel %q", tc.path, got, tc.status, tc.rel)
			}
			if tc.status == "present" && (got.Size != 5 || got.MTime == 0) {
				t.Fatalf("present stat missing size/mtime: %+v", got)
			}
		})
	}
}
