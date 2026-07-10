package agents

import "testing"

func TestArtifactServeContentType(t *testing.T) {
	if ct, inline := artifactServeContentType("a.png"); !inline || ct != "image/png" {
		t.Errorf("png inline got ct=%q inline=%v", ct, inline)
	}
	if ct, inline := artifactServeContentType("a.pdf"); !inline || ct != "application/pdf" {
		t.Errorf("pdf inline got ct=%q inline=%v", ct, inline)
	}
	if ct, inline := artifactServeContentType("a.svg"); !inline || ct != "image/svg+xml" {
		t.Errorf("svg inline got ct=%q inline=%v", ct, inline)
	}
	if _, inline := artifactServeContentType("a.html"); inline {
		t.Errorf("html must NOT be inline (download to avoid same-origin exec)")
	}
	if _, inline := artifactServeContentType("a.zip"); inline {
		t.Errorf("zip must NOT be inline")
	}
}

func TestScoreFilePath(t *testing.T) {
	// A missing term rules the path out entirely (AND semantics).
	if _, ok := scoreFilePath("src/main.go", []string{"src", "zzz"}); ok {
		t.Errorf("path with a missing term must not match")
	}
	// All terms present → matches.
	if _, ok := scoreFilePath("src/main.go", []string{"src", "main"}); !ok {
		t.Errorf("path containing every term must match")
	}
	// Empty query matches everything (browsable default).
	if _, ok := scoreFilePath("anything.txt", nil); !ok {
		t.Errorf("empty query must match")
	}
	// A basename hit ranks above a same-term dir-only hit (lower score = better).
	base, _ := scoreFilePath("main.go", []string{"main"})
	dir, _ := scoreFilePath("main/x.go", []string{"main"})
	if !(base < dir) {
		t.Errorf("basename match should rank better: base=%d dir=%d", base, dir)
	}
	// Among equal-quality matches, the shorter path wins.
	short, _ := scoreFilePath("a/util.go", []string{"util"})
	long, _ := scoreFilePath("a/deep/util.go", []string{"util"})
	if !(short < long) {
		t.Errorf("shorter path should rank better: short=%d long=%d", short, long)
	}
}
