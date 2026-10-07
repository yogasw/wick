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

// TestDiag_SpaceResolution checks every space id in play and tries the property
// write with each, plus a version-bump variant — hunting the shape Notion accepts.
func TestDiag_SpaceResolution(t *testing.T) {
	dbID := os.Getenv("NOTION_TEST_DB_ID")
	if dbID == "" || os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2") == "" {
		t.Skip("env not set")
	}
	cp := testCtx(t, map[string]string{"parent_type": "database", "parent_id": dbID, "title": "wick diag space (hapus)"})
	rowOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	rowID := rowOut.(map[string]any)["id"].(string)

	cl, _ := newClient(testCtx(t, nil))
	idSpace, _, _ := cl.identity()
	rm, _ := cl.syncRecordValues([]string{rowID})
	rec := recordValue(rm.Block[rowID])
	rowSpace := strField(rec, "space_id")
	collID := strField(rec, "parent_id")

	crm, err := cl.syncRecords("collection", []string{collID})
	if err != nil {
		t.Fatalf("collection: %v", err)
	}
	coll := recordValue(crm.Collection[collID])
	collSpace := strField(coll, "space_id")
	collParent := strField(coll, "parent_id")
	t.Logf("idSpace=%s rowSpace=%s collSpace=%s collParent=%s", idSpace, rowSpace, collSpace, collParent)

	nameToID, idToType, _ := cl.fetchCollectionSchema(collID)
	pid := nameToID["Activity"]
	val, _ := formatProperty(idToType[pid], "Debug")
	var v any
	_ = json.Unmarshal(val, &v)

	for _, s := range []struct{ name, space string }{
		{"identity", idSpace}, {"row", rowSpace}, {"collection", collSpace},
	} {
		if s.space == "" {
			continue
		}
		err := cl.saveTransactions(s.space, []map[string]any{
			{"pointer": map[string]any{"table": "block", "id": rowID, "spaceId": s.space},
				"path": []any{"properties", pid}, "command": "set", "args": v},
		})
		st := "OK  "
		if err != nil {
			st = "FAIL"
		}
		t.Logf("%s  space=%-11s %s", st, s.name, s.space)
	}

	// Does a plain TEXT block property (not collection schema) still work on this row?
	if err := cl.saveTransactions(idSpace, []map[string]any{
		{"pointer": map[string]any{"table": "block", "id": rowID, "spaceId": idSpace},
			"path": []any{"properties", "title"}, "command": "set", "args": [][]any{{"wick diag space (hapus) 2"}}},
	}); err != nil {
		t.Logf("FAIL  title control %v", err)
	} else {
		t.Logf("OK    title control")
	}
}
