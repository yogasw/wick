package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func projectDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func op(t *testing.T, dir, name, rel, data string) (string, error) {
	t.Helper()
	b, err := FileOp(dir, name, rel, data)
	return string(b), err
}

func TestFileOpWriteReadListExists(t *testing.T) {
	dir := projectDir(t)
	if got, err := op(t, dir, "write", "out/ho.json", `{"a":1}`); err != nil || got != `{"bytes":7,"path":"out/ho.json"}` {
		t.Fatalf("write = %s, %v", got, err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "codemode", "out", "ho.json"))
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("on disk = %q, %v", b, err)
	}
	if got, err := op(t, dir, "read", "out/ho.json", ""); err != nil || got != `"{\"a\":1}"` {
		t.Fatalf("read = %s, %v", got, err)
	}
	if got, _ := op(t, dir, "exists", "out/ho.json", ""); got != "true" {
		t.Fatalf("exists = %s", got)
	}
	if got, _ := op(t, dir, "exists", "nope.json", ""); got != "false" {
		t.Fatalf("exists missing = %s", got)
	}
	var list []FileEntry
	got, err := op(t, dir, "list", "", "")
	if err != nil || json.Unmarshal([]byte(got), &list) != nil || len(list) != 1 || list[0].Path != "out/ho.json" || list[0].Size != 7 || list[0].Modified == "" {
		t.Fatalf("list = %s, %v", got, err)
	}
	if got, _ := op(t, dir, "list", "other", ""); got != "[]" {
		t.Fatalf("list of a missing folder = %s", got)
	}
	// Nothing outside codemode/ was touched.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "codemode" {
		t.Fatalf("project folder holds %v", entries)
	}
}

func TestFileOpBeforeFolderExists(t *testing.T) {
	dir := projectDir(t)
	if _, err := op(t, dir, "read", "a.json", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("read = %v", err)
	}
	if got, err := op(t, dir, "list", "", ""); err != nil || got != "[]" {
		t.Fatalf("list = %s, %v", got, err)
	}
}

func TestFileOpRefusesEscapes(t *testing.T) {
	dir := projectDir(t)
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	_ = os.WriteFile(outside, []byte("s"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "project.txt"), []byte("p"), 0o644)
	cm := filepath.Join(dir, "codemode")
	_ = os.MkdirAll(cm, 0o755)
	_ = os.Symlink(outside, filepath.Join(cm, "link.txt"))
	_ = os.Symlink(filepath.Dir(dir), filepath.Join(cm, "up"))
	_ = os.Symlink(filepath.Join(dir, "project.txt"), filepath.Join(cm, "proj.txt"))

	for _, c := range []struct{ op, rel string }{
		{"read", "../project.txt"},
		{"read", "a/../../project.txt"},
		{"read", outside},
		{"read", "~/x"},
		{"read", "link.txt"},
		{"read", "proj.txt"}, // still the project, but outside codemode/
		{"exists", "link.txt"},
		{"list", "up"},
		{"write", "../x.txt"},
		{"write", "up/x.txt"},
		{"write", "link.txt"},
	} {
		if _, err := op(t, dir, c.op, c.rel, "data"); !errors.Is(err, ErrFileDenied) {
			t.Errorf("%s %s: err = %v, want denied", c.op, c.rel, err)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "s" {
		t.Fatalf("outside file changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x.txt")); err == nil {
		t.Fatal("write escaped the folder")
	}
}

func TestFileOpCodemodeSymlinkRefused(t *testing.T) {
	dir := projectDir(t)
	elsewhere := t.TempDir()
	_ = os.Symlink(elsewhere, filepath.Join(dir, "codemode"))
	if _, err := op(t, dir, "write", "a.txt", "x"); !errors.Is(err, ErrFileDenied) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "a.txt")); err == nil {
		t.Fatal("wrote through a symlinked codemode/")
	}
}

func TestFileOpCaps(t *testing.T) {
	dir := projectDir(t)
	big := strings.Repeat("x", MaxFileBytes+1)
	if _, err := op(t, dir, "write", "big.txt", big); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("write big = %v", err)
	}
	cm := filepath.Join(dir, "codemode")
	_ = os.MkdirAll(cm, 0o755)
	_ = os.WriteFile(filepath.Join(cm, "big.txt"), []byte(big), 0o644)
	if _, err := op(t, dir, "read", "big.txt", ""); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("read big = %v", err)
	}
	if _, err := op(t, dir, "read", ".", ""); err == nil {
		t.Fatal("read of the folder itself passed")
	}

	// Folder total: fill to just under the cap, then one more MB is refused.
	fill := strings.Repeat("y", MaxFileBytes)
	for i := 0; i < MaxFolderBytes/MaxFileBytes-2; i++ {
		_ = os.WriteFile(filepath.Join(cm, "f"+string(rune('a'+i))+".txt"), []byte(fill), 0o644)
	}
	if _, err := op(t, dir, "write", "one-more.txt", fill); err == nil || !strings.Contains(err.Error(), "would pass") {
		t.Fatalf("over folder cap = %v", err)
	}
	// Overwriting an existing file counts only the difference.
	if _, err := op(t, dir, "write", "fa.txt", "small"); err != nil {
		t.Fatalf("overwrite = %v", err)
	}
}

func TestFileOpFileCountCap(t *testing.T) {
	dir := projectDir(t)
	cm := filepath.Join(dir, "codemode", "many")
	_ = os.MkdirAll(cm, 0o755)
	for i := 0; i < MaxFiles; i++ {
		_ = os.WriteFile(filepath.Join(cm, "f"+strings.Repeat("0", 4)+strconv.Itoa(i)), nil, 0o644)
	}
	if _, err := op(t, dir, "write", "new.txt", "x"); err == nil || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("over file cap = %v", err)
	}
	if _, err := op(t, dir, "write", "many/f0000"+strconv.Itoa(0), "x"); err != nil {
		t.Fatalf("overwrite at cap = %v", err)
	}
}

func TestFileOpNoProject(t *testing.T) {
	if _, err := FileOp("", "read", "a.json", ""); err == nil || !strings.Contains(err.Error(), "project folder") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunFileEventsEndToEnd(t *testing.T) {
	f := &fakeRunner{}
	h := hostFor(t, f)
	dir := projectDir(t)
	res, err := h.Run(context.Background(), Request{Lang: "js", Dir: dir, Code: strings.Join([]string{
		`wick.file write items.json [1,2]`,
		`wick.file read items.json`,
		`wick.file read ../escape.txt`,
		`CALL conn:a/list`,
	}, "\n")})
	if err != nil || !res.OK {
		t.Fatalf("run = %+v, %v", res, err)
	}
	var out string
	_ = json.Unmarshal(res.Result, &out)
	if !strings.Contains(out, `"[1,2]"`) || !strings.Contains(out, "ERR wick.file ../escape.txt: must stay inside codemode/") {
		t.Fatalf("result = %s", out)
	}
	ids := map[string]bool{}
	for _, c := range res.Calls {
		ids[c.ToolID] = c.OK
	}
	if ok, found := ids["file:write codemode/items.json"]; !found || !ok {
		t.Fatalf("calls = %+v", res.Calls)
	}
	if ok, found := ids["file:read codemode/../escape.txt"]; !found || ok {
		t.Fatalf("calls = %+v", res.Calls)
	}
	if len(res.Calls) != 4 {
		t.Fatalf("calls = %+v", res.Calls)
	}
}

func TestRunFileNeedsProject(t *testing.T) {
	f := &fakeRunner{}
	h := hostFor(t, f)
	res, err := h.Run(context.Background(), Request{Lang: "js", Code: "wick.file read a.json", NoDir: "set workspace"})
	if err != nil || res.OK || !strings.Contains(res.Error, "no project folder: set workspace") || f.next.Load() != 0 {
		t.Fatalf("run = %+v, %v", res, err)
	}
}

func TestRunFileOldRunner(t *testing.T) {
	f := &fakeRunner{oldRunner: true}
	h := hostFor(t, f)
	res, err := h.Run(context.Background(), Request{Lang: "js", Dir: projectDir(t), Code: "const x = wick.file.read('a.json')"})
	if err != nil || res.OK || !strings.Contains(res.Error, "runner too old, update codemode_goja") {
		t.Fatalf("run = %+v, %v", res, err)
	}
}
