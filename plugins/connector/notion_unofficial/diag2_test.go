// SOLVED 2026-09-28 — kept as the record of how, not as a live question.
// The answer is updateBlockPropertyValue (see propOp in repo.go and the
// recorded payload in testdata/captured-property-edit.json). Guessing command
// names, which is what several of these probes do, never found it; recording
// the web client's own request did. Every test here is env-gated and skips.
package main

import (
	"os"
	"testing"
)

// TestDiag_WhichPropertiesAccept walks every writable column of the test database
// and tries a single-cell write on a throwaway row, so we can see whether the 400
// depends on the property (id characters, type) or hits all of them equally.
func TestDiag_WhichPropertiesAccept(t *testing.T) {
	dbID := os.Getenv("NOTION_TEST_DB_ID")
	if dbID == "" || os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2") == "" {
		t.Skip("env not set")
	}
	cp := testCtx(t, map[string]string{
		"parent_type": "database", "parent_id": dbID, "title": "wick diag props (hapus)",
	})
	rowOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	rowID := rowOut.(map[string]any)["id"].(string)
	t.Logf("row: %s", rowID)

	cl, _ := newClient(testCtx(t, nil))
	spaceID, _, _ := cl.identity()
	rm, _ := cl.syncRecordValues([]string{rowID})
	collID := strField(recordValue(rm.Block[rowID]), "parent_id")
	nameToID, idToType, err := cl.fetchCollectionSchema(collID)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}

	sample := map[string]string{
		"select": "Debug", "status": "Debug", "text": "diag", "title": "diag",
		"number": "1", "url": "https://example.com", "checkbox": "true",
		"date": "2026-09-28 10:00", "multi_select": "diag",
	}
	for name, id := range nameToID {
		typ := idToType[id]
		v, settable := formatProperty(typ, sample[typ])
		if !settable {
			continue
		}
		err := cl.saveTransactions(spaceID, []map[string]any{
			op("block", rowID, spaceID, []any{"properties", id}, "set", v),
		})
		status := "OK  "
		if err != nil {
			status = "FAIL"
		}
		t.Logf("%s  name=%-28q id=%-8q type=%-14s", status, name, id, typ)
	}
}
