package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// Integration tests hit Notion's LIVE private API. They are skipped unless
// NOTION_UNOFFICIAL_TOKEN_V2 is set, so CI (and anyone without a cookie) stays
// green. Run locally with:
//
//	NOTION_UNOFFICIAL_TOKEN_V2=<token_v2> \
//	NOTION_TEST_PAGE_ID=6f4139a1-cae6-4bef-97d6-56d537e7ce73 \
//	go test ./connector/notion_unofficial/ -run Integration -v
//
// The page id defaults to the shared "Website" database if unset. These tests
// only READ (fetch, query, get_records, status) — no writes, so they leave no
// artifacts. set_title is exercised separately below and gated on its own env.

const defaultTestPageID = "6f4139a1-cae6-4bef-97d6-56d537e7ce73"

func testCtx(t *testing.T, inputs map[string]string) *connector.Ctx {
	t.Helper()
	token := os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2")
	if token == "" {
		t.Skip("NOTION_UNOFFICIAL_TOKEN_V2 not set — skipping live integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	if inputs == nil {
		inputs = map[string]string{}
	}
	configs := map[string]string{
		"token_v2":       token,
		"active_user_id": os.Getenv("NOTION_UNOFFICIAL_ACTIVE_USER_ID"),
		// The usage-note gate blocks every agent-facing op while blank; fill it so
		// the live tests exercise the real ops rather than the refusal.
		"usage_note": "integration test",
	}
	return connector.NewPluginCtx(ctx, configs, inputs)
}

func testPageID() string {
	if id := os.Getenv("NOTION_TEST_PAGE_ID"); id != "" {
		return id
	}
	return defaultTestPageID
}

// TestIntegration_ImportCurl proves the paste-a-cURL path works end to end:
// importExtract parses a real cURL into config fields, and a client built from
// those fields connects live. Uses NOTION_TEST_IMPORT_CURL (a full Copy-as-cURL).
func TestIntegration_ImportCurl(t *testing.T) {
	curl := os.Getenv("NOTION_TEST_IMPORT_CURL")
	if curl == "" {
		t.Skip("NOTION_TEST_IMPORT_CURL not set — skipping import-curl integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	// 1) Extract op turns the pasted cURL into config fields.
	ec := connector.NewPluginCtx(ctx, map[string]string{}, map[string]string{"raw": curl})
	out, err := importExtract(ec)
	if err != nil {
		t.Fatalf("importExtract: %v", err)
	}
	m := out.(map[string]any)
	fields, _ := m["fields"].(map[string]string)
	if fields["token_v2"] == "" {
		t.Fatalf("expected token_v2 in extracted fields, got: %v (html=%v)", fields, m["html"])
	}
	t.Logf("extracted fields: %v", keysOf(fields))

	// 2) A client built from those fields connects live.
	sc := connector.NewPluginCtx(ctx, fields, map[string]string{})
	st, err := connectionStatus(sc)
	if err != nil {
		t.Fatalf("connectionStatus: %v", err)
	}
	html := st.(map[string]any)["html"].(string)
	if !strings.Contains(html, "Connected") {
		t.Fatalf("expected connected status from extracted fields, got: %s", html)
	}
	t.Logf("status: %s", html)
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestIntegration_ConnectionStatus(t *testing.T) {
	c := testCtx(t, nil)
	out, err := connectionStatus(c)
	if err != nil {
		t.Fatalf("connectionStatus: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	html, _ := m["html"].(string)
	if !strings.Contains(html, "Connected") {
		t.Fatalf("expected a connected status card, got: %s", html)
	}
	t.Logf("status card: %s", html)
}

func TestIntegration_Fetch(t *testing.T) {
	c := testCtx(t, map[string]string{"page_id": testPageID()})
	out, err := fetch(c)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	m := out.(map[string]any)
	if m["id"] == "" {
		t.Fatal("fetch returned empty id")
	}
	t.Logf("title=%q content_md(len)=%d", m["title"], len(m["content_md"].(string)))
}

func TestIntegration_QueryDatabase(t *testing.T) {
	c := testCtx(t, map[string]string{"page_id": testPageID(), "limit": "5"})
	out, err := queryDatabase(c)
	if err != nil {
		t.Fatalf("queryDatabase: %v", err)
	}
	m := out.(map[string]any)
	rows, _ := m["rows"].([]map[string]any)
	t.Logf("rows returned: %d", len(rows))
	if len(rows) > 0 {
		t.Logf("first row: title=%q cells=%v", rows[0]["title"], rows[0]["cells"])
	}
}

// TestIntegration_CreatePageAndComment creates a throwaway subpage under
// NOTION_TEST_WRITE_PAGE_ID, comments on it, then archives it (via set on
// alive=false is not exposed, so it stays — logged for manual cleanup). Gated on
// the write env so read-only runs never write.
func TestIntegration_CreatePageAndComment(t *testing.T) {
	parent := os.Getenv("NOTION_TEST_WRITE_PAGE_ID")
	if parent == "" {
		t.Skip("NOTION_TEST_WRITE_PAGE_ID not set — skipping write test")
	}

	cp := testCtx(t, map[string]string{
		"parent_type": "page",
		"parent_id":   parent,
		"title":       "wick create_page test (hapus)",
	})
	pageOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	newID := pageOut.(map[string]any)["id"].(string)
	t.Logf("created page: %s", newID)

	cc := testCtx(t, map[string]string{"page_id": newID, "text": "comment from wick integration test"})
	comOut, err := createComment(cc)
	if err != nil {
		t.Fatalf("createComment: %v", err)
	}
	t.Logf("created comment: %v", comOut)
	t.Logf("NOTE: manually delete the test page %s afterward", newID)
}

// TestIntegration_SetTitle is destructive (renames a page), so it needs an
// explicit throwaway page id AND opt-in via NOTION_TEST_WRITE_PAGE_ID. It
// restores the original title afterward.
func TestIntegration_SetTitle(t *testing.T) {
	pageID := os.Getenv("NOTION_TEST_WRITE_PAGE_ID")
	if pageID == "" {
		t.Skip("NOTION_TEST_WRITE_PAGE_ID not set — skipping write test")
	}
	// read current title to restore later
	rc := testCtx(t, map[string]string{"page_id": pageID})
	before, err := fetch(rc)
	if err != nil {
		t.Fatalf("fetch before: %v", err)
	}
	orig := before.(map[string]any)["title"].(string)

	c := testCtx(t, map[string]string{"page_id": pageID, "title": "wick set_title test"})
	if _, err := setTitle(c); err != nil {
		t.Fatalf("setTitle: %v", err)
	}
	// restore
	rc2 := testCtx(t, map[string]string{"page_id": pageID, "title": orig})
	if _, err := setTitle(rc2); err != nil {
		t.Logf("warning: failed to restore title to %q: %v", orig, err)
	}
}
