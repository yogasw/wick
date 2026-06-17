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
