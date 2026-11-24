package buildsource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const revision = "0123456789012345678901234567890123456789"

func TestSourceComparison(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/repos/yogasw/wick/compare/"+revision+"...develop":
			_ = json.NewEncoder(w).Encode(map[string]int{"ahead_by": 7, "behind_by": 2})
		case "/repos/yogasw/wick/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"tag_name":"v1.4.0"}, {"tag_name":"v1.2.1"},
				{"tag_name":"v1.2.0"}, {"tag_name":"v1.1.0"},
				{"tag_name":"v1.5.0", "draft":true},
				{"tag_name":"v1.6.0-rc.1", "prerelease":true},
				{"tag_name":"plugins/v1.9.0"},
			})
		default: http.NotFound(w,r)
		}
	}))
	defer srv.Close()
	c := NewChecker(Metadata{Kind:"source", Repository:OfficialRepo, Revision:revision, CompareRef:"develop", SourceVersion:"v1.2.0"}, "")
	c.api = srv.URL
	s := c.Snapshot(context.Background())
	if s.Behind == nil || *s.Behind != 7 || s.Ahead == nil || *s.Ahead != 2 { t.Fatalf("bad direction: %+v",s) }
	if s.NewerReleases == nil || *s.NewerReleases != 2 || s.LatestRelease != "v1.4.0" { t.Fatalf("bad releases: %+v",s) }
	_ = c.Snapshot(context.Background())
	if requests != 2 { t.Fatalf("expected cached response, got %d requests", requests) }
}

func TestReleaseComparison(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		switch r.URL.Path {
		case "/repos/yogasw/wick/commits/v1.2.0":
			_ = json.NewEncoder(w).Encode(map[string]any{"sha":revision,"commit":map[string]any{"committer":map[string]string{"date":"2026-11-24T00:00:00Z"}}})
		case "/repos/yogasw/wick/compare/"+revision+"...master":
			_ = json.NewEncoder(w).Encode(map[string]int{"ahead_by":0,"behind_by":0})
		case "/repos/yogasw/wick/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name":"v1.2.0"}})
		default: http.NotFound(w,r)
		}
	}))
	defer srv.Close()
	c := NewChecker(Current("v1.2.0","v1.2.0",OfficialRepo),"")
	c.api=srv.URL
	s:=c.Snapshot(context.Background())
	if s.Kind!="release" || s.Revision!=revision || s.CommitDate=="" || s.Tag!="v1.2.0" { t.Fatalf("bad release: %+v",s) }
	if s.Behind==nil || *s.Behind!=0 || s.NewerReleases==nil || *s.NewerReleases!=0 { t.Fatalf("bad counts: %+v",s) }
}

func TestMissingOriginIsNotZero(t *testing.T) {
	srv:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){http.NotFound(w,r)}))
	defer srv.Close()
	c:=NewChecker(Metadata{Kind:"source",Repository:OfficialRepo,Revision:revision,CompareRef:"develop",SourceVersion:"v1.2.0"},"")
	c.api=srv.URL
	s:=c.Snapshot(context.Background())
	if s.Behind!=nil || s.Ahead!=nil || s.NewerReleases!=nil || s.CompareError=="" || s.ReleaseError=="" { t.Fatalf("unknown must not look current: %+v",s) }
}

func TestRepositoryAndVersion(t *testing.T) {
	for _, remote := range []string{"git@github.com:yogasw/wick.git","https://user:password@github.com/yogasw/wick.git","ssh://git@github.com/yogasw/wick.git"} {
		if got:=GitHubRepository(remote);got!=OfficialRepo { t.Fatalf("wrong repository: %s",got) }
	}
	if GitHubRepository("https://example.com/yogasw/wick.git")!="" { t.Fatal("non-GitHub remote accepted") }
	if StableVersion("1.2.3")!="v1.2.3" || StableVersion("1.2.3.34-source-build")!="" { t.Fatal("bad version normalization") }
	if Current("v1.2.0","v1.2.0.34-source-build","").Kind!="source" { t.Fatal("source build misclassified as release") }
}
