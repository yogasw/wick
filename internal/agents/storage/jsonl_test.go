package storage

import (
	"path/filepath"
	"testing"
)

func TestAppendJSONLAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")

	for i := 0; i < 3; i++ {
		if err := AppendJSONL(path, "test-format", "S1", map[string]int{"i": i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	var lines []string
	if err := ReadJSONL(path, func(line []byte) bool {
		lines = append(lines, string(line))
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}

	count, err := CountJSONLEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("count: got %d", count)
	}
}

func TestTailJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	for i := 0; i < 5; i++ {
		_ = AppendJSONL(path, "f", "s", map[string]int{"i": i})
	}
	var got []string
	if err := TailJSONL(path, 2, func(line []byte) {
		got = append(got, string(line))
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("tail 2: got %d", len(got))
	}
}

func TestTruncateJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	_ = AppendJSONL(path, "f", "s", map[string]int{"i": 1})
	_ = AppendJSONL(path, "f", "s", map[string]int{"i": 2})
	if err := TruncateJSONL(path, "f", "s"); err != nil {
		t.Fatal(err)
	}
	count, _ := CountJSONLEntries(path)
	if count != 0 {
		t.Fatalf("after truncate: got %d entries, want 0", count)
	}
}

func TestReadJSONLMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.jsonl")
	called := false
	if err := ReadJSONL(path, func(line []byte) bool { called = true; return true }); err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if called {
		t.Fatalf("emit called for missing file")
	}
}
