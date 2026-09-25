package aimemory

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// The editing half, pinned against real 2.4.0 output (see manage.go's header
// for the captures these fixtures come from).

func joinArgs(a []string) string { return strings.Join(a, " ") }

// TestWritePageHandsTheBodyOverStdin: the body must NOT be on argv. A page is
// arbitrary markdown — too big for Windows' command line, and visible in the
// process list all the way to the disk.
func TestWritePageHandsTheBodyOverStdin(t *testing.T) {
	st := &stubRun{out: "✓ wrote notes/hello.md (page_id=01a0d8dd) under wick/demo"}
	body := "# Hello\n\nkasir_prod_db is a read replica."

	res, err := newSource(st).WritePage(context.Background(), agentmemory.Conn{DataDir: "/srv/mem"},
		agentmemory.ReadScope{Workspace: "wick", Project: "demo"},
		agentmemory.PageWrite{Path: "notes/hello.md", Body: body, Title: "Hello", Kind: "fact"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	args := joinArgs(st.args)
	if !strings.Contains(args, "--body -") {
		t.Fatalf("the body did not go over stdin: %q", args)
	}
	if strings.Contains(args, "kasir_prod_db") {
		t.Fatalf("the page body is on argv: %q", args)
	}
	if string(st.stdin) != body {
		t.Fatalf("stdin was %q, want the body", st.stdin)
	}
	for _, want := range []string{"write-page", "--workspace wick", "--project demo", "--path notes/hello.md", "--title Hello", "--kind fact", "--data-dir /srv/mem"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %q", want, args)
		}
	}
	// write-page has no --json mode; passing it would be rejected.
	if strings.Contains(args, "--json") {
		t.Errorf("write-page must not be given --json: %q", args)
	}
	if res.PageID != "01a0d8dd" || res.Path != "notes/hello.md" {
		t.Fatalf("result %+v", res)
	}
	if !strings.Contains(res.Output, "under wick/demo") {
		t.Fatalf("the backend's own line is what names the scope the write landed in: %q", res.Output)
	}
}

// TestWritePageSurvivesALineWithoutAnID: the id is a nicety; a build that
// stops printing it must not turn a successful write into an error.
func TestWritePageSurvivesALineWithoutAnID(t *testing.T) {
	st := &stubRun{out: "✓ wrote notes/hello.md under wick/demo"}
	res, err := newSource(st).WritePage(context.Background(), agentmemory.Conn{},
		agentmemory.ReadScope{Workspace: "wick", Project: "demo"},
		agentmemory.PageWrite{Path: "notes/hello.md", Body: "x"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if res.PageID != "" {
		t.Fatalf("page id %q invented from a line that has none", res.PageID)
	}
}

// TestDeletePageNamesWhatItRemoved: "deleted" with no path is the answer that
// leaves someone wondering which page went.
func TestDeletePageNamesWhatItRemoved(t *testing.T) {
	st := &stubRun{out: "✓ deleted notes/hello.md under wick/demo"}
	res, err := newSource(st).DeletePage(context.Background(), agentmemory.Conn{},
		agentmemory.ReadScope{Workspace: "wick", Project: "demo"}, "notes/hello.md")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Path != "notes/hello.md" || !res.Deleted {
		t.Fatalf("result %+v", res)
	}
	if !strings.Contains(res.Output, "deleted notes/hello.md") {
		t.Fatalf("output %q", res.Output)
	}
	args := joinArgs(st.args)
	if !strings.Contains(args, "delete-page") || !strings.Contains(args, "--workspace wick") || !strings.Contains(args, "--project demo") {
		t.Fatalf("the scope must be explicit on a delete: %q", args)
	}
	if strings.Contains(args, "--json") {
		t.Errorf("delete-page has no --json mode: %q", args)
	}
}

const checkpointsFixture = `[
  {"oid":"d544bc66fa5e33c4da76b37982e251da373b9a33","short_oid":"d544bc66fa5e","time":1790344784,"summary":"write-page wick/demo: notes/hello.md"},
  {"oid":"c84640f0a0f56e380d84adafd0a7e861ae2ac3b8","short_oid":"c84640f0a0f5","time":1790344371,"summary":"session 01a0c33c: hai"}
]`

func TestCheckpointsParse(t *testing.T) {
	st := &stubRun{out: checkpointsFixture}
	rows, err := newSource(st).Checkpoints(context.Background(), agentmemory.Conn{}, 5)
	if err != nil {
		t.Fatalf("checkpoints: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d checkpoints", len(rows))
	}
	if rows[0].ShortOID != "d544bc66fa5e" || rows[0].TimeUnix != 1790344784 {
		t.Fatalf("row %+v", rows[0])
	}
	// The summary is what makes a checkpoint choosable rather than a hash.
	if !strings.Contains(rows[0].Summary, "notes/hello.md") {
		t.Fatalf("summary %q", rows[0].Summary)
	}
	if args := joinArgs(st.args); !strings.Contains(args, "--limit 5") || !strings.Contains(args, "--json") {
		t.Fatalf("args %q", args)
	}
}

const restoreFixture = `{
  "page_id": "01a0d8dd-9832-7411-a1f0-dd548406863d",
  "path": "notes/hello.md",
  "restored_from": "d544bc66fa5e33c4da76b37982e251da373b9a33",
  "pre_checkpoint": null,
  "checkpoint": "25d150d14afd7f364ce38963958fc8600da11d1a"
}`

// TestRestorePageParse: pre_checkpoint arrives as null when the page did not
// exist before the restore, and that must not become the string "<nil>".
func TestRestorePageParse(t *testing.T) {
	st := &stubRun{out: restoreFixture}
	res, err := newSource(st).RestorePage(context.Background(), agentmemory.Conn{},
		agentmemory.ReadScope{Workspace: "wick", Project: "demo"}, "notes/hello.md", "d544bc66fa5e")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if res.Path != "notes/hello.md" || res.Checkpoint == "" || res.RestoredFrom == "" {
		t.Fatalf("result %+v", res)
	}
	if res.PreCheckpoint != "" {
		t.Fatalf("pre_checkpoint was null and became %q", res.PreCheckpoint)
	}
	if args := joinArgs(st.args); !strings.Contains(args, "--from d544bc66fa5e") || !strings.Contains(args, "--json") {
		t.Fatalf("args %q", args)
	}
}

// TestManageCallsRefuseAnEmptyTarget: a missing path would otherwise reach
// the CLI, which resolves it from somewhere else entirely.
func TestManageCallsRefuseAnEmptyTarget(t *testing.T) {
	src := newSource(&stubRun{})
	sc := agentmemory.ReadScope{Workspace: "wick", Project: "demo"}
	if _, err := src.WritePage(context.Background(), agentmemory.Conn{}, sc, agentmemory.PageWrite{Body: "x"}); err == nil {
		t.Error("write-page accepted an empty path")
	}
	if _, err := src.DeletePage(context.Background(), agentmemory.Conn{}, sc, "  "); err == nil {
		t.Error("delete-page accepted an empty path")
	}
	if _, err := src.RestorePage(context.Background(), agentmemory.Conn{}, sc, "notes/x.md", ""); err == nil {
		t.Error("restore-page accepted an empty checkpoint")
	}
}
