package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The end of a log is the part that says what happened, so a payload that
// is too long keeps its tail. Truncating the other way would report the
// moment a test run started and hide the failure it ended on.
func TestTodoDetailBodyKeepsTheTail(t *testing.T) {
	body := strings.Repeat("a", 9000) + "FAIL: TestThing"
	got, err := todoDetailBody(body, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "FAIL: TestThing") {
		t.Error("the tail was dropped — that is where the verdict is")
	}
	if !strings.HasPrefix(got, "[truncated:") {
		t.Error("a truncated payload must say so")
	}
}

func TestTodoDetailBodyReadsAFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "gate.log")
	if err := os.WriteFile(p, []byte("ok internal/admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := todoDetailBody("", p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ok internal/admin\n" {
		t.Errorf("body = %q, want the file's contents", got)
	}
}

// --detail and --detail-file are two answers to one question; taking both
// would mean silently picking one.
func TestTodoDetailBodyRefusesBothSources(t *testing.T) {
	if _, err := todoDetailBody("inline", "/tmp/x"); err == nil {
		t.Error("passing both --detail and --detail-file was accepted")
	}
}

// No payload is the normal case: most updates are just a status and a
// counter.
func TestTodoDetailBodyEmptyIsFine(t *testing.T) {
	got, err := todoDetailBody("", "")
	if err != nil || got != "" {
		t.Errorf("empty detail = %q, %v — want it accepted", got, err)
	}
}
