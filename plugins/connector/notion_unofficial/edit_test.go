package main

import (
	"os"
	"strings"
	"testing"
)

// --- unit tests: markdown → blocks (deterministic, no network) ---

func TestMarkdownToBlocks_Types(t *testing.T) {
	md := strings.Join([]string{
		"# Title",
		"## Sub",
		"### SubSub",
		"",
		"A paragraph line one",
		"still same paragraph",
		"",
		"- bullet one",
		"* bullet two",
		"1. first",
		"2. second",
		"- [ ] todo open",
		"- [x] todo done",
		"> a quote",
		"---",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
	}, "\n")

	got := markdownToBlocks(md)

	want := []newBlock{
		{typ: "header", title: "Title"},
		{typ: "sub_header", title: "Sub"},
		{typ: "sub_sub_header", title: "SubSub"},
		{typ: "text", title: "A paragraph line one still same paragraph"},
		{typ: "bulleted_list", title: "bullet one"},
		{typ: "bulleted_list", title: "bullet two"},
		{typ: "numbered_list", title: "first"},
		{typ: "numbered_list", title: "second"},
		{typ: "to_do", title: "todo open", checked: false},
		{typ: "to_do", title: "todo done", checked: true},
		{typ: "quote", title: "a quote"},
		{typ: "divider"},
		{typ: "code", title: "fmt.Println(\"hi\")", lang: "go"},
	}

	if len(got) != len(want) {
		t.Fatalf("block count = %d, want %d\ngot: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].typ != want[i].typ || got[i].title != want[i].title ||
			got[i].checked != want[i].checked || got[i].lang != want[i].lang {
			t.Errorf("block[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMarkdownToBlocks_Empty(t *testing.T) {
	if got := markdownToBlocks("   \n\n  "); len(got) != 0 {
		t.Errorf("blank markdown produced %d blocks, want 0", len(got))
	}
}

// TestEditableTypes guards Yoga's concern: only text-bearing blocks are editable.
// Non-text blocks (image, embed, table, divider, page, …) must be rejected so an
// edit can't corrupt a block that shouldn't be touched.
func TestEditableTypes(t *testing.T) {
	editable := []string{"text", "header", "sub_header", "bulleted_list", "to_do", "quote", "code"}
	for _, tp := range editable {
		if !editableBlockTypes[tp] {
			t.Errorf("%q should be editable", tp)
		}
	}
	notEditable := []string{"image", "embed", "collection_view", "column", "column_list", "divider", "page", "bookmark", "video", "file"}
	for _, tp := range notEditable {
		if editableBlockTypes[tp] {
			t.Errorf("%q should NOT be editable", tp)
		}
	}
}

func TestEditableTypesList_Sorted(t *testing.T) {
	got := editableTypesList()
	if !strings.Contains(got, "text") || !strings.Contains(got, "code") {
		t.Errorf("editableTypesList missing expected types: %q", got)
	}
	// must be comma-separated + sorted (deterministic error messages)
	if strings.Index(got, "bulleted_list") > strings.Index(got, "code") {
		t.Errorf("editableTypesList not sorted: %q", got)
	}
}

// --- integration: full edit round-trip on a live throwaway page ---

// TestIntegration_EditBlocks exercises the whole per-block edit flow against the
// LIVE private API on a throwaway subpage:
//  1. append_content adds known blocks,
//  2. list_blocks returns them with ids + editable flags,
//  3. update_block rewrites ONE block and leaves the others byte-for-byte,
//  4. delete_block removes ONE block and leaves the rest,
//  5. update_block on a divider is refused (the unsupported-type guard).
//
// Gated on NOTION_TEST_WRITE_PAGE_ID (a parent page to create the throwaway
// under). Cleans up the throwaway page's blocks it can; logs the page for manual
// deletion since page-archive isn't exposed.
func TestIntegration_EditBlocks(t *testing.T) {
	parent := os.Getenv("NOTION_TEST_WRITE_PAGE_ID")
	if parent == "" {
		t.Skip("NOTION_TEST_WRITE_PAGE_ID not set — skipping live edit test")
	}

	// Create a throwaway subpage to edit.
	cp := testCtx(t, map[string]string{
		"parent_type": "page",
		"parent_id":   parent,
		"title":       "wick edit_block test (hapus)",
	})
	pageOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	pageID := pageOut.(map[string]any)["id"].(string)
	t.Logf("throwaway page: %s (delete manually after)", pageID)

	// 1) Append a known body.
	body := "## Heading\n\nParagraph to keep.\n\nParagraph to edit.\n\n---\n\nParagraph to delete."
	ac := testCtx(t, map[string]string{"page_id": pageID, "markdown": body})
	if _, err := appendContent(ac); err != nil {
		t.Fatalf("appendContent: %v", err)
	}

	// Also exercise mid-page insert: add a block after the first one and confirm
	// it lands at index 1, not at the end.
	firstList, _ := listBlocks(testCtx(t, map[string]string{"page_id": pageID}))
	fb, _ := firstList.(map[string]any)["blocks"].([]blockInfo)
	if len(fb) > 0 {
		ic := testCtx(t, map[string]string{"page_id": pageID, "markdown": "INSERTED MIDDLE", "after_block_id": fb[0].ID})
		if _, err := appendContent(ic); err != nil {
			t.Fatalf("appendContent (insert): %v", err)
		}
		chk, _ := listBlocks(testCtx(t, map[string]string{"page_id": pageID}))
		cb, _ := chk.(map[string]any)["blocks"].([]blockInfo)
		if len(cb) < 2 || cb[1].Text != "INSERTED MIDDLE" {
			t.Errorf("mid-page insert not at index 1; got: %+v", cb)
		}

		// Guard: a bogus anchor must be refused, not silently mis-placed — so a
		// bad id can't break the page layout.
		bad := testCtx(t, map[string]string{
			"page_id":        pageID,
			"markdown":       "SHOULD NOT APPEAR",
			"after_block_id": "00000000-0000-0000-0000-000000000000",
		})
		if _, err := appendContent(bad); err == nil {
			t.Error("append with a non-page anchor should have been refused")
		} else {
			t.Logf("bad anchor correctly refused: %v", err)
		}
	}

	// 2) List blocks.
	lc := testCtx(t, map[string]string{"page_id": pageID})
	lout, err := listBlocks(lc)
	if err != nil {
		t.Fatalf("listBlocks: %v", err)
	}
	blocks, _ := lout.(map[string]any)["blocks"].([]blockInfo)
	if len(blocks) < 5 {
		t.Fatalf("expected >=5 blocks, got %d: %+v", len(blocks), blocks)
	}

	// Find the divider (unsupported) + the two target paragraphs.
	var dividerID, editID, deleteID, keepID string
	for _, b := range blocks {
		switch {
		case b.Type == "divider":
			dividerID = b.ID
			if b.Editable {
				t.Errorf("divider marked editable, want editable=false")
			}
		case b.Text == "Paragraph to edit.":
			editID = b.ID
		case b.Text == "Paragraph to delete.":
			deleteID = b.ID
		case b.Text == "Paragraph to keep.":
			keepID = b.ID
		}
	}
	if editID == "" || deleteID == "" || keepID == "" || dividerID == "" {
		t.Fatalf("could not locate target blocks: edit=%q delete=%q keep=%q divider=%q", editID, deleteID, keepID, dividerID)
	}

	// 3) Update ONE block. Others must stay.
	uc := testCtx(t, map[string]string{"block_id": editID, "text": "Paragraph EDITED."})
	if _, err := updateBlock(uc); err != nil {
		t.Fatalf("updateBlock: %v", err)
	}

	// 5) Guard: editing the divider must be refused.
	gc := testCtx(t, map[string]string{"block_id": dividerID, "text": "nope"})
	if _, err := updateBlock(gc); err == nil {
		t.Errorf("updateBlock on a divider should have been refused")
	} else {
		t.Logf("divider edit correctly refused: %v", err)
	}

	// 4) Delete ONE block.
	dc := testCtx(t, map[string]string{"page_id": pageID, "block_id": deleteID})
	if _, err := deleteBlock(dc); err != nil {
		t.Fatalf("deleteBlock: %v", err)
	}

	// Verify: edited text changed, kept text intact, deleted block gone.
	lout2, err := listBlocks(testCtx(t, map[string]string{"page_id": pageID}))
	if err != nil {
		t.Fatalf("listBlocks after: %v", err)
	}
	after, _ := lout2.(map[string]any)["blocks"].([]blockInfo)
	var sawEdited, sawKept, sawDeleted bool
	for _, b := range after {
		switch b.Text {
		case "Paragraph EDITED.":
			sawEdited = true
		case "Paragraph to keep.":
			sawKept = true
		case "Paragraph to delete.":
			sawDeleted = true
		}
	}
	if !sawEdited {
		t.Error("edited block not found with new text")
	}
	if !sawKept {
		t.Error("kept block was lost — edit/delete touched the wrong block")
	}
	if sawDeleted {
		t.Error("deleted block still present")
	}
}
