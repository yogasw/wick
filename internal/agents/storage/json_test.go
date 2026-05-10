package storage

import (
	"path/filepath"
	"testing"
)

func TestWriteReadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "meta.json")

	in := map[string]any{"name": "abc", "n": 3}
	if err := WriteJSON(path, in); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var out map[string]any
	if err := ReadJSON(path, &out); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	if out["name"] != "abc" {
		t.Fatalf("name: got %v", out["name"])
	}
}

func TestWriteJSONAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	if err := WriteJSON(path, map[string]int{"v": 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, map[string]int{"v": 2}); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := ReadJSON(path, &got); err != nil {
		t.Fatal(err)
	}
	if got["v"] != 2 {
		t.Fatalf("v: got %d", got["v"])
	}
}
