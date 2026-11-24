//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func TestFileCRUDAndExpectedHash(t *testing.T) {
	name := filepath.Join(t.TempDir(), "source.go")
	if err := writeNew(name, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := writeNew(name, []byte("overwrite")); err == nil {
		t.Fatal("create overwrote file")
	}
	if err := replaceFile(name, []byte("edited"), "stale"); err == nil {
		t.Fatal("stale edit accepted")
	}
	b, err := readFile(name)
	if err != nil || string(b) != "original" {
		t.Fatalf("%s %v", b, err)
	}
	if err := replaceFile(name, []byte("edited"), digest(b)); err != nil {
		t.Fatal(err)
	}
	b, err = readFile(name)
	if err != nil || string(b) != "edited" {
		t.Fatalf("%s %v", b, err)
	}
	if err := removeFile(name, false); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(name); err == nil {
		t.Fatal("file still exists")
	}
}

func TestSymlinkRefused(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(filepath.Join(alias, "secret")); err == nil {
		t.Fatal("directory symlink followed")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(link); err == nil {
		t.Fatal("file symlink followed")
	}
	if err := replaceFile(link, []byte("changed"), digest([]byte("private"))); err == nil {
		t.Fatal("symlink edit accepted")
	}
}

func TestHardlinkRefused(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(root, "hard")
	if err := os.Link(secret, hard); err != nil {
		// Android may deny hardlink creation at the OS boundary. Verify
		// the denied fixture was not created; keep the Linux regression.
		if runtime.GOOS == "android" && errors.Is(err, os.ErrPermission) {
			if _, statErr := os.Lstat(hard); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("unexpected hardlink state: %v", statErr)
			}
			t.Skipf("Android prohibits hardlink fixture creation: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := readFile(hard); err == nil {
		t.Fatal("hardlink accepted")
	}
	if err := removeFile(hard, false); err == nil {
		t.Fatal("hardlink removal accepted")
	}
}

func TestSearchFiltersDeniedFoldersAndContent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "private"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public.txt", ".env", "private/secret.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("needle"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := testPolicy(t, root)
	raw, _ := json.Marshal(p)
	c := connector.NewPluginCtx(t.Context(), map[string]string{"policy_json": string(raw)}, map[string]string{"path": root, "query": "needle"})
	out, err := fileAction("search", func(p Policy, n string, c *connector.Ctx) (any, error) { return search(p, n, c, true, false) })(c)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if !strings.Contains(string(b), "public.txt") || strings.Contains(string(b), "secret.txt") || strings.Contains(string(b), ".env") {
		t.Fatal(string(b))
	}
}

func TestDeleteDirectoryIsNotRecursive(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeFile(root, true); err == nil {
		t.Fatal("non-empty directory deleted")
	}
}

func TestTraverseSearchableAncestor(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor")
	child := filepath.Join(ancestor, "child")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ancestor, 0100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(ancestor, 0700); err != nil {
			t.Error(err)
		}
	})

	name := filepath.Join(child, "file")
	if err := writeNew(name, []byte("content")); err != nil {
		t.Fatalf("write through searchable ancestor: %v", err)
	}
	names, more, err := listDir(child, 10)
	if err != nil || more || len(names) != 1 || names[0] != "file" {
		t.Fatalf("list child: %v, %v, %v", names, more, err)
	}
	if os.Geteuid() != 0 {
		if _, _, err := listDir(ancestor, 10); err == nil {
			t.Fatal("listing unreadable ancestor unexpectedly allowed")
		}
	}
}
