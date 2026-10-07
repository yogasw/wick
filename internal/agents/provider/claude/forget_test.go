package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeWorkspace(t *testing.T) {
	got := EncodeWorkspace("/home/ubuntu/.support-tools/agents/projects/8c28-ab/files")
	want := "-home-ubuntu--support-tools-agents-projects-8c28-ab-files"
	if got != want {
		t.Fatalf("EncodeWorkspace = %q, want %q", got, want)
	}
	if got := EncodeWorkspace("/a/b_c.d"); got != "-a-b-c-d" {
		t.Fatalf("EncodeWorkspace(/a/b_c.d) = %q", got)
	}
}

func TestForgetWorkspaceExactAndChildrenOnly(t *testing.T) {
	cfg := t.TempDir()
	root := filepath.Join(cfg, "projects")
	cwd := "/w/projects/uuid1/files"
	enc := EncodeWorkspace(cwd)
	gone := []string{enc, enc + "-wick", enc + "-repo-sub"}
	kept := []string{
		EncodeWorkspace("/w/projects/uuid2/files"),
		EncodeWorkspace("/w/projects/uuid1"),        // the project dir itself, not its cwd
		EncodeWorkspace("/w/projects/uuid1/filesX"), // shares the prefix without a '-'
		EncodeWorkspace("/w/projects/uuid10/files"),
	}
	for _, d := range append(append([]string{}, gone...), kept...) {
		if err := os.MkdirAll(filepath.Join(root, d, "memory"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := ForgetWorkspace(cfg, cwd); err != nil {
		t.Fatal(err)
	}
	for _, d := range gone {
		if _, err := os.Stat(filepath.Join(root, d)); !os.IsNotExist(err) {
			t.Fatalf("%s survived: %v", d, err)
		}
	}
	for _, d := range kept {
		if _, err := os.Stat(filepath.Join(root, d)); err != nil {
			t.Fatalf("%s was removed: %v", d, err)
		}
	}
}

func TestForgetWorkspaceRefusesRootAndMissingDir(t *testing.T) {
	if err := ForgetWorkspace(t.TempDir(), "/"); err == nil {
		t.Fatal("forgetting / must be refused")
	}
	if err := ForgetWorkspace("", "/x"); err == nil {
		t.Fatal("empty config dir must be refused")
	}
	if err := ForgetWorkspace(t.TempDir(), "/x/files"); err != nil {
		t.Fatalf("no projects/ dir: %v", err)
	}
}
