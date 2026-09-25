package aimemory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// Payloads are verbatim captures from the running daemon (2026-09-25).
const (
	projectsFixture = `[{"workspace_name":"default","project_name":"proj2","page_count":6,"last_updated":"2026-09-24T17:04:40.440131Z"},
	{"workspace_name":"default","project_name":"scratch","page_count":0,"last_updated":null}]`
	searchFixture = `[{"workspace":"default","project":"proj2","path":"sessions/eab3c800.md","title":"Ini project baru 'proj2'…",
	"kind":"session","snippet":"# Ini project baru '<mark>proj2</mark>'…","rank":-1.3750620067643742e-6}]`
)

// TestProjectsParse pins the REST project list, including the null
// last_updated a project with no pages carries.
func TestProjectsParse(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(projectsFixture))
	}))
	defer srv.Close()

	rows, err := source{}.Projects(context.Background(), agentmemory.Conn{BaseURL: srv.URL, WebEnabled: true})
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if gotPath != "/api/v1/projects" {
		t.Fatalf("path: %q", gotPath)
	}
	if len(rows) != 2 || rows[0].Project != "proj2" || rows[0].PageCount != 6 {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[1].LastUpdated != "" {
		t.Fatalf("a null last_updated must read as empty, got %q", rows[1].LastUpdated)
	}
}

// TestProjectsWithoutWebAPI: there is no CLI that lists projects, so with the
// web API off this must name the cause rather than return an empty list the
// user would read as "no memory stored".
func TestProjectsWithoutWebAPI(t *testing.T) {
	_, err := source{}.Projects(context.Background(), agentmemory.Conn{BaseURL: "http://127.0.0.1:1"})
	if !errors.Is(err, agentmemory.ErrWebDisabled) {
		t.Fatalf("want ErrWebDisabled, got %v", err)
	}
}

// TestRESTNotFoundIsWebDisabled: with --enable-web off the whole /api/v1 tree
// is absent, so a 404 on these three paths means the API is off — not that a
// project is missing.
func TestRESTNotFoundIsWebDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := source{}.Projects(context.Background(), agentmemory.Conn{BaseURL: srv.URL, WebEnabled: true})
	if !errors.Is(err, agentmemory.ErrWebDisabled) {
		t.Fatalf("want ErrWebDisabled, got %v", err)
	}
}

// TestSearchREST checks the query is forwarded with its scope and that the
// <mark> markup in a snippet survives untouched — the FE renders it.
func TestSearchREST(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(searchFixture))
	}))
	defer srv.Close()

	hits, err := source{}.Search(context.Background(),
		agentmemory.Conn{BaseURL: srv.URL, WebEnabled: true},
		agentmemory.ReadScope{Project: "proj2"}, "proj2")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "project=proj2&q=proj2" {
		t.Fatalf("query: %q", gotQuery)
	}
	if len(hits) != 1 || hits[0].Path != "sessions/eab3c800.md" || hits[0].Workspace != "default" {
		t.Fatalf("hits: %+v", hits)
	}
	if hits[0].Snippet == "" || hits[0].Rank == 0 {
		t.Fatalf("snippet/rank lost: %+v", hits[0])
	}
}

// TestSearchFallsBackToCLI: with the web API off the search box still works,
// through `search --json`. Those rows carry no workspace/project, so the
// requested scope is stamped back on — the scope the command actually ran
// under, not a guess.
func TestSearchFallsBackToCLI(t *testing.T) {
	st := &stubRun{out: `[{"path":"sessions/eab3c800.md","title":"t","snippet":"s","rank":-1.0e-6}]`}
	hits, err := newSource(st).Search(context.Background(),
		agentmemory.Conn{BaseURL: "http://127.0.0.1:1"},
		agentmemory.ReadScope{Workspace: "qiscus", Project: "wick-8c28230d"}, "kasir_prod_db")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Workspace != "qiscus" || hits[0].Project != "wick-8c28230d" {
		t.Fatalf("hits: %+v", hits)
	}
	if st.args[len(st.args)-2] != "kasir_prod_db" {
		t.Fatalf("query must be the positional argument before --json, got %v", st.args)
	}
}
