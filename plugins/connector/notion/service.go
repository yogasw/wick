package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const apiBase = "https://api.notion.com/v1"

// --- op handlers (handler → service). Each validates, then calls the REST/
// normalizer helpers below. ---

func search(c *connector.Ctx) (any, error) {
	size := c.InputInt("page_size")
	if size <= 0 {
		size = 25
	}
	if size > 100 {
		size = 100
	}
	body := map[string]any{
		"page_size": size,
		"sort":      map[string]any{"direction": "descending", "timestamp": "last_edited_time"},
	}
	if q := strings.TrimSpace(c.Input("query")); q != "" {
		body["query"] = q
	}
	if ot := strings.TrimSpace(c.Input("object_type")); ot == "page" || ot == "database" {
		body["filter"] = map[string]any{"value": ot, "property": "object"}
	}

	raw, err := notionDo(c, http.MethodPost, "/search", body)
	if err != nil {
		return nil, err
	}
	var env struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	out := make([]searchHit, 0, len(env.Results))
	for _, r := range env.Results {
		if hit, ok := searchHitFrom(r); ok {
			out = append(out, hit)
		}
	}
	return map[string]any{"count": len(out), "results": out}, nil
}

func fetch(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("id"))
	if id == "" {
		return nil, errors.New("id is required")
	}

	// Try page first; if it's a database the page endpoint 404s, so fall back.
	rawPage, pageErr := notionDo(c, http.MethodGet, "/pages/"+id, nil)
	if pageErr == nil {
		rec, err := pageToRecord(rawPage)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"type": "page", "meta": rec}
		withContent := c.InputBool("with_content")
		withBlocks := c.InputBool("with_blocks")
		// One walk feeds both outputs, so we never pay extra API calls for the
		// second view. content_md (default) is the clean read; blocks[] (opt-in)
		// carries the IDs needed to target a block for a comment or edit.
		if withContent || withBlocks {
			md, blocks, err := pageContent(c, id, withBlocks)
			if err != nil {
				return nil, err
			}
			if withContent {
				result["content_md"] = md
			}
			if withBlocks {
				result["blocks"] = blocks
			}
		}
		return result, nil
	}

	rawDB, dbErr := notionDo(c, http.MethodGet, "/databases/"+id, nil)
	if dbErr == nil {
		return databaseToRecord(rawDB)
	}
	// Neither worked — surface the page error (usually the more useful 404 hint).
	return nil, pageErr
}

func queryDatabase(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("database_id"))
	if id == "" {
		return nil, errors.New("database_id is required")
	}
	size := c.InputInt("page_size")
	if size <= 0 || size > 100 {
		size = 100
	}
	limit := c.InputInt("limit")
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	base := map[string]any{"page_size": size}
	if f := strings.TrimSpace(c.Input("filter")); f != "" {
		var v any
		if err := json.Unmarshal([]byte(f), &v); err != nil {
			return nil, fmt.Errorf("filter is not valid JSON: %w", err)
		}
		base["filter"] = v
	}
	if s := strings.TrimSpace(c.Input("sorts")); s != "" {
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("sorts is not valid JSON: %w", err)
		}
		base["sorts"] = v
	}

	rows := make([]pageRecord, 0, size)
	cursor := ""
	for len(rows) < limit {
		body := map[string]any{}
		for k, v := range base {
			body[k] = v
		}
		if cursor != "" {
			body["start_cursor"] = cursor
		}
		raw, err := notionDo(c, http.MethodPost, "/databases/"+id+"/query", body)
		if err != nil {
			return nil, err
		}
		var env struct {
			Results    []json.RawMessage `json:"results"`
			HasMore    bool              `json:"has_more"`
			NextCursor string            `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode query: %w", err)
		}
		for _, r := range env.Results {
			rec, err := pageToRecord(r)
			if err != nil {
				continue
			}
			rows = append(rows, rec)
			if len(rows) >= limit {
				break
			}
		}
		if !env.HasMore || env.NextCursor == "" {
			break
		}
		cursor = env.NextCursor
	}
	return map[string]any{"count": len(rows), "rows": rows}, nil
}

func getComments(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("block_id"))
	if id == "" {
		return nil, errors.New("block_id is required")
	}
	raw, err := notionDo(c, http.MethodGet, "/comments?block_id="+url.QueryEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var env struct {
		Results []struct {
			ID           string          `json:"id"`
			DiscussionID string          `json:"discussion_id"`
			CreatedTime  string          `json:"created_time"`
			RichText     json.RawMessage `json:"rich_text"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}
	out := make([]map[string]any, 0, len(env.Results))
	for _, r := range env.Results {
		out = append(out, map[string]any{
			"id":            r.ID,
			"discussion_id": r.DiscussionID,
			"created_time":  r.CreatedTime,
			"text":          richTextToPlain(r.RichText),
		})
	}
	return map[string]any{"count": len(out), "comments": out}, nil
}

func getUsers(c *connector.Ctx) (any, error) {
	q := strings.ToLower(strings.TrimSpace(c.Input("query")))
	out := make([]map[string]any, 0)
	cursor := ""
	for {
		path := "/users?page_size=100"
		if cursor != "" {
			path += "&start_cursor=" + url.QueryEscape(cursor)
		}
		raw, err := notionDo(c, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var env struct {
			Results []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Type   string `json:"type"`
				Person struct {
					Email string `json:"email"`
				} `json:"person"`
			} `json:"results"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode users: %w", err)
		}
		for _, u := range env.Results {
			if q != "" && !strings.Contains(strings.ToLower(u.Name), q) && !strings.Contains(strings.ToLower(u.Person.Email), q) {
				continue
			}
			out = append(out, map[string]any{
				"id": u.ID, "name": u.Name, "type": u.Type, "email": u.Person.Email,
			})
		}
		if !env.HasMore || env.NextCursor == "" {
			break
		}
		cursor = env.NextCursor
	}
	return map[string]any{"count": len(out), "users": out}, nil
}

func createPage(c *connector.Ctx) (any, error) {
	parentType := strings.TrimSpace(c.Input("parent_type"))
	parentID := normalizeID(c.Input("parent_id"))
	title := strings.TrimSpace(c.Input("title"))
	if parentID == "" || title == "" {
		return nil, errors.New("parent_id and title are required")
	}

	var parent map[string]any
	switch parentType {
	case "database":
		parent = map[string]any{"database_id": parentID}
	case "page":
		parent = map[string]any{"page_id": parentID}
	default:
		return nil, errors.New("parent_type must be database or page")
	}

	props := map[string]any{}
	if extra := strings.TrimSpace(c.Input("properties")); extra != "" {
		if err := json.Unmarshal([]byte(extra), &props); err != nil {
			return nil, fmt.Errorf("properties is not valid JSON: %w", err)
		}
	}
	// The title property key differs per DB, but "title" and "Name" are the
	// common cases; only set it if the caller didn't already provide one.
	if !hasTitleProp(props) {
		props[titleKeyFor(parentType)] = map[string]any{
			"title": []any{map[string]any{"text": map[string]any{"content": title}}},
		}
	}

	body := map[string]any{"parent": parent, "properties": props}
	if md := strings.TrimSpace(c.Input("content")); md != "" {
		body["children"] = markdownToBlocks(md)
	}

	raw, err := notionDo(c, http.MethodPost, "/pages", body)
	if err != nil {
		return nil, err
	}
	return idURLResult(raw)
}

func updatePage(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	if id == "" {
		return nil, errors.New("page_id is required")
	}

	if c.InputBool("archive") {
		raw, err := notionDo(c, http.MethodPatch, "/pages/"+id, map[string]any{"in_trash": true})
		if err != nil {
			return nil, err
		}
		return idURLResult(raw)
	}

	var lastRaw []byte
	if props := strings.TrimSpace(c.Input("properties")); props != "" {
		var p map[string]any
		if err := json.Unmarshal([]byte(props), &p); err != nil {
			return nil, fmt.Errorf("properties is not valid JSON: %w", err)
		}
		raw, err := notionDo(c, http.MethodPatch, "/pages/"+id, map[string]any{"properties": p})
		if err != nil {
			return nil, err
		}
		lastRaw = raw
	}
	if md := strings.TrimSpace(c.Input("append_md")); md != "" {
		_, err := notionDo(c, http.MethodPatch, "/blocks/"+id+"/children",
			map[string]any{"children": markdownToBlocks(md)})
		if err != nil {
			return nil, err
		}
	}
	if lastRaw != nil {
		return idURLResult(lastRaw)
	}
	return map[string]any{"id": id}, nil
}

func createComment(c *connector.Ctx) (any, error) {
	text := strings.TrimSpace(c.Input("text"))
	if text == "" {
		return nil, errors.New("text is required")
	}

	body := map[string]any{
		"rich_text": []any{map[string]any{"text": map[string]any{"content": text}}},
	}
	// Target precedence, verified live against the REST API on 2022-06-28:
	//   - reply to a thread: discussion_id at the TOP LEVEL (NOT inside parent).
	//   - comment on a specific block/text: parent.block_id.
	//   - page-level: parent.page_id.
	switch {
	case strings.TrimSpace(c.Input("discussion_id")) != "":
		body["discussion_id"] = normalizeID(c.Input("discussion_id"))
	case strings.TrimSpace(c.Input("block_id")) != "":
		body["parent"] = map[string]any{"block_id": normalizeID(c.Input("block_id"))}
	case strings.TrimSpace(c.Input("page_id")) != "":
		body["parent"] = map[string]any{"page_id": normalizeID(c.Input("page_id"))}
	default:
		return nil, errors.New("provide one of discussion_id, block_id, or page_id")
	}

	raw, err := notionDo(c, http.MethodPost, "/comments", body)
	if err != nil {
		return nil, err
	}
	var v struct {
		ID           string `json:"id"`
		DiscussionID string `json:"discussion_id"`
	}
	_ = json.Unmarshal(raw, &v)
	return map[string]any{"id": v.ID, "discussion_id": v.DiscussionID}, nil
}

func createDatabase(c *connector.Ctx) (any, error) {
	parentID := normalizeID(c.Input("parent_page_id"))
	title := strings.TrimSpace(c.Input("title"))
	schema := strings.TrimSpace(c.Input("schema"))
	if parentID == "" || title == "" || schema == "" {
		return nil, errors.New("parent_page_id, title, and schema are required")
	}
	var props map[string]any
	if err := json.Unmarshal([]byte(schema), &props); err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %w", err)
	}
	body := map[string]any{
		"parent":     map[string]any{"type": "page_id", "page_id": parentID},
		"title":      []any{map[string]any{"text": map[string]any{"content": title}}},
		"properties": props,
	}
	raw, err := notionDo(c, http.MethodPost, "/databases", body)
	if err != nil {
		return nil, err
	}
	return idURLResult(raw)
}

func updateDatabase(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("database_id"))
	if id == "" {
		return nil, errors.New("database_id is required")
	}
	body := map[string]any{}
	if t := strings.TrimSpace(c.Input("title")); t != "" {
		body["title"] = []any{map[string]any{"text": map[string]any{"content": t}}}
	}
	if props := strings.TrimSpace(c.Input("properties")); props != "" {
		var p map[string]any
		if err := json.Unmarshal([]byte(props), &p); err != nil {
			return nil, fmt.Errorf("properties is not valid JSON: %w", err)
		}
		body["properties"] = p
	}
	if len(body) == 0 {
		return nil, errors.New("nothing to update: provide title and/or properties")
	}
	raw, err := notionDo(c, http.MethodPatch, "/databases/"+id, body)
	if err != nil {
		return nil, err
	}
	var v struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &v)
	return map[string]any{"id": v.ID}, nil
}

// --- config-only widget ---

func connectionStatus(c *connector.Ctx) (any, error) {
	raw, err := notionDo(c, http.MethodGet, "/users/me", nil)
	if err != nil {
		return map[string]any{"html": statusCard(false, html.EscapeString(shorten(err.Error(), 160)))}, nil
	}
	var me struct {
		Name string `json:"name"`
		Bot  struct {
			WorkspaceName string `json:"workspace_name"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(raw, &me); err != nil {
		return map[string]any{"html": statusCard(true, "Connected, but couldn't read bot details.")}, nil
	}
	parts := []string{"Connected"}
	if me.Name != "" {
		parts = append(parts, "bot "+html.EscapeString(me.Name))
	}
	if me.Bot.WorkspaceName != "" {
		parts = append(parts, "workspace "+html.EscapeString(me.Bot.WorkspaceName))
	}
	return map[string]any{"html": statusCard(true, strings.Join(parts, " · "))}, nil
}

// --- HTTP layer: the ONE place that talks to api.notion.com ---

// notionDo performs an authenticated request against the Notion REST API and
// returns the raw body on 2xx, or a formatted error carrying Notion's own
// message on non-2xx. body==nil sends no payload.
func notionDo(c *connector.Ctx, method, path string, body any) ([]byte, error) {
	token := strings.TrimSpace(c.Cfg("token"))
	if token == "" {
		return nil, errors.New("token is not configured")
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(c.Context(), method, apiBase+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Notion-Version", notionVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call notion: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notionError(resp.StatusCode, raw)
	}
	return raw, nil
}

func notionError(status int, body []byte) error {
	var env struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &env) == nil && env.Message != "" {
		if env.Code != "" {
			return fmt.Errorf("notion %d (%s): %s", status, env.Code, env.Message)
		}
		return fmt.Errorf("notion %d: %s", status, env.Message)
	}
	if msg := strings.TrimSpace(string(body)); msg != "" {
		return fmt.Errorf("notion %d: %s", status, shorten(msg, 300))
	}
	return fmt.Errorf("notion %d", status)
}

// idURLResult pulls {id,url} out of a create/patch response.
func idURLResult(raw []byte) (any, error) {
	var v struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return map[string]any{"id": v.ID, "url": v.URL}, nil
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
