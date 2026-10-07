// SOLVED 2026-09-28 — kept as the record of how, not as a live question.
// The answer is updateBlockPropertyValue (see propOp in repo.go and the
// recorded payload in testdata/captured-property-edit.json). Guessing command
// names, which is what several of these probes do, never found it; recording
// the web client's own request did. Every test here is env-gated and skips.
package main

import (
	"encoding/json"
	"os"
	"testing"
)

// TestDiag_PropertyWriteVariants bisects the 400 that update_page_properties
// started returning: it creates one throwaway row, then tries the SAME cell
// write several ways and reports which shapes Notion accepts. Diagnostic only —
// gated on the same env as the other live tests, skipped in CI.
func TestDiag_PropertyWriteVariants(t *testing.T) {
	dbID := os.Getenv("NOTION_TEST_DB_ID")
	if dbID == "" || os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2") == "" {
		t.Skip("NOTION_TEST_DB_ID / NOTION_UNOFFICIAL_TOKEN_V2 not set")
	}
	prop := os.Getenv("NOTION_TEST_PROP")
	val := os.Getenv("NOTION_TEST_PROP_VALUE")

	// A row to experiment on.
	cp := testCtx(t, map[string]string{
		"parent_type": "database",
		"parent_id":   dbID,
		"title":       "wick diag 400 (hapus)",
	})
	rowOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	rowID := rowOut.(map[string]any)["id"].(string)
	t.Logf("row: %s", rowID)

	cl, err := newClient(testCtx(t, nil))
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	spaceID, userID, err := cl.identity()
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	t.Logf("identity: space=%s user=%s", spaceID, userID)

	// Resolve the row's collection + the property id, exactly as updatePageProps does.
	rm, err := cl.syncRecordValues([]string{rowID})
	if err != nil {
		t.Fatalf("syncRecordValues: %v", err)
	}
	rec := recordValue(rm.Block[rowID])
	parentTable := strField(rec, "parent_table")
	collID := strField(rec, "parent_id")
	rowSpace := strField(rec, "space_id")
	t.Logf("row: parent_table=%s parent_id=%s space_id=%s", parentTable, collID, rowSpace)
	if rowSpace != "" && rowSpace != spaceID {
		t.Logf("!! row space_id differs from identity space_id — pointer spaceId may be wrong")
	}

	nameToID, idToType, err := cl.fetchCollectionSchema(collID)
	if err != nil {
		t.Fatalf("fetchCollectionSchema: %v", err)
	}
	propID := nameToID[prop]
	t.Logf("property %q -> id=%q type=%q", prop, propID, idToType[propID])
	if propID == "" {
		t.Fatalf("property %q not found in schema", prop)
	}
	value, _ := formatProperty(idToType[propID], val)

	try := func(label string, run func() error) {
		if err := run(); err != nil {
			t.Logf("FAIL  %-42s %v", label, err)
			return
		}
		t.Logf("OK    %-42s", label)
	}

	// 1. Control: the title write that set_title performs — known to work.
	try("A title write (control)", func() error {
		return cl.saveTransactions(spaceID, []map[string]any{
			op("block", rowID, spaceID, []any{"properties", "title"}, "set", [][]any{{"wick diag 400 (hapus)"}}),
		})
	})

	// 2. Current implementation: set one schema property by id.
	try("B set ["+`"properties",<id>`+"]", func() error {
		return cl.saveTransactions(spaceID, []map[string]any{
			op("block", rowID, spaceID, []any{"properties", propID}, "set", value),
		})
	})

	// 3. Same, but pointer spaceId taken from the ROW instead of identity.
	if rowSpace != "" {
		try("C set with row's own space_id", func() error {
			return cl.saveTransactions(rowSpace, []map[string]any{
				op("block", rowID, rowSpace, []any{"properties", propID}, "set", value),
			})
		})
	}

	// 4. "update" command on the whole properties map instead of "set" on one cell.
	try("D update [\"properties\"] map", func() error {
		var v any
		_ = json.Unmarshal(value, &v)
		return cl.saveTransactions(spaceID, []map[string]any{
			op("block", rowID, spaceID, []any{"properties"}, "update", map[string]any{propID: v}),
		})
	})

	// 5. "update" on the block root with a properties map.
	try("E update [] with properties map", func() error {
		var v any
		_ = json.Unmarshal(value, &v)
		return cl.saveTransactions(spaceID, []map[string]any{
			op("block", rowID, spaceID, []any{}, "update", map[string]any{
				"properties": map[string]any{propID: v},
			}),
		})
	})

	// 6. Property write accompanied by a last_edited bump, like the web app sends.
	try("F set + last_edited_time bump", func() error {
		now := nowMillis()
		return cl.saveTransactions(spaceID, []map[string]any{
			op("block", rowID, spaceID, []any{"properties", propID}, "set", value),
			op("block", rowID, spaceID, []any{"last_edited_time"}, "set", now),
			op("block", rowID, spaceID, []any{"last_edited_by_id"}, "set", userID),
			op("block", rowID, spaceID, []any{"last_edited_by_table"}, "set", "notion_user"),
		})
	})

	// 7. The fanout endpoint the current web app uses.
	try("G saveTransactionsFanout", func() error {
		body := map[string]any{
			"requestId": newUUID(),
			"transactions": []any{map[string]any{
				"id":      newUUID(),
				"spaceId": spaceID,
				"operations": []map[string]any{
					op("block", rowID, spaceID, []any{"properties", propID}, "set", value),
				},
			}},
		}
		_, err := cl.post("/saveTransactionsFanout", body)
		return err
	})
}
