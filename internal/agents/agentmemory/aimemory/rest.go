package aimemory

// The HTTP half of the reader. ai-memory's REST API is three endpoints and no
// more — /api/v1/workspaces, /api/v1/projects, /api/v1/search — and all three
// exist only when the daemon runs with --enable-web; everything else under
// /api/v1 is a 404 by design, so nothing here goes looking (PLAN §13.2.1).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/agentmemory"
)

// restTimeout bounds one API call. These are local SQLite-backed reads on
// loopback; a slow one means the daemon is wedged, not busy.
const restTimeout = 10 * time.Second

// maxRESTBody caps what a reply may be. Search snippets are short and the
// project list is small, so a body past this is a wrong endpoint, not data.
const maxRESTBody = 8 << 20

// Projects lists the store's projects. There is no CLI equivalent — no
// `projects` subcommand exists — so with the web API off this is genuinely
// unanswerable, and saying so is better than an empty table that reads as
// "you have no memory".
func (s source) Projects(ctx context.Context, conn agentmemory.Conn) ([]agentmemory.ProjectRow, error) {
	if !conn.WebEnabled {
		return nil, agentmemory.ErrWebDisabled
	}
	var rows []projectJSON
	if err := getJSON(ctx, conn, "/api/v1/projects", nil, &rows); err != nil {
		return nil, err
	}
	out := make([]agentmemory.ProjectRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, agentmemory.ProjectRow{
			Workspace:   r.Workspace,
			Project:     r.Project,
			PageCount:   r.PageCount,
			LastUpdated: str(r.LastUpdated),
		})
	}
	return out, nil
}

// Search returns wiki hits for q.
//
// The REST endpoint is preferred because it labels each hit with the
// workspace and project it came from, which the CLI's output omits. When the
// web API is off wick falls back to `search --json`, losing those two labels
// but keeping the feature usable in the default configuration — a search box
// that only works after turning on a second server would be a strange thing to
// ship.
func (s source) Search(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, q string) ([]agentmemory.SearchHit, error) {
	if !conn.WebEnabled {
		return s.searchCLI(ctx, conn, sc, q)
	}
	params := url.Values{"q": {q}}
	if sc.Workspace != "" {
		params.Set("workspace", sc.Workspace)
	}
	if sc.Project != "" {
		params.Set("project", sc.Project)
	}
	var rows []searchJSON
	if err := getJSON(ctx, conn, "/api/v1/search", params, &rows); err != nil {
		return nil, err
	}
	out := make([]agentmemory.SearchHit, 0, len(rows))
	for _, r := range rows {
		out = append(out, agentmemory.SearchHit{
			Workspace: r.Workspace,
			Project:   r.Project,
			Path:      r.Path,
			Title:     r.Title,
			Kind:      r.Kind,
			Snippet:   r.Snippet,
			Rank:      r.Rank,
		})
	}
	return out, nil
}

// searchCLI is the no-web fallback. Its rows carry no workspace/project, so
// the scope the caller asked for is stamped back on — that is the scope the
// command was run under, not a guess.
func (s source) searchCLI(ctx context.Context, conn agentmemory.Conn, sc agentmemory.ReadScope, q string) ([]agentmemory.SearchHit, error) {
	out, err := s.run(ctx, sc.Dir, env(conn), args("search", conn, sc, q), nil)
	if err != nil {
		return nil, err
	}
	var rows []searchJSON
	if err := decode(out, &rows); err != nil {
		return nil, fmt.Errorf("parse %s search: %w", binName, err)
	}
	hits := make([]agentmemory.SearchHit, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, agentmemory.SearchHit{
			Workspace: sc.Workspace,
			Project:   sc.Project,
			Path:      r.Path,
			Title:     r.Title,
			Kind:      r.Kind,
			Snippet:   r.Snippet,
			Rank:      r.Rank,
		})
	}
	return hits, nil
}

// getJSON performs one API GET and decodes it.
//
// A 404 is translated to ErrWebDisabled: with --enable-web off the whole
// /api/v1 tree is absent, and that is the only way these three paths answer
// 404 on a daemon that is otherwise up. Reporting it as "not found" would send
// the user looking for a missing project.
func getJSON(ctx context.Context, conn agentmemory.Conn, path string, params url.Values, v any) error {
	ctx, cancel := context.WithTimeout(ctx, restTimeout)
	defer cancel()

	u := strings.TrimRight(conn.BaseURL, "/") + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// Machine APIs authenticate with Authorization: Bearer (the same token as
	// AI_MEMORY_AUTH_TOKEN); wick's managed daemon runs unauthenticated on
	// loopback, so this only fires for an instance pointed elsewhere.
	if conn.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+conn.AuthToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRESTBody))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return agentmemory.ErrWebDisabled
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s %s: %s: %s", binName, path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

// projectJSON is one row of /api/v1/projects.
type projectJSON struct {
	Workspace   string  `json:"workspace_name"`
	Project     string  `json:"project_name"`
	PageCount   int64   `json:"page_count"`
	LastUpdated *string `json:"last_updated"`
}

// searchJSON is one hit. The REST and CLI documents share every field the
// panel uses; only workspace/project are REST-only, which is why the CLI
// fallback fills them from the requested scope.
type searchJSON struct {
	Workspace string  `json:"workspace"`
	Project   string  `json:"project"`
	Path      string  `json:"path"`
	Title     string  `json:"title"`
	Kind      string  `json:"kind"`
	Snippet   string  `json:"snippet"`
	Rank      float64 `json:"rank"`
}
