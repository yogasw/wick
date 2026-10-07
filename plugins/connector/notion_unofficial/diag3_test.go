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

	"github.com/yogasw/wick/pkg/connector"
)

// TestDiag_PointerVariants tries pointer/transaction shapes for a collection-column
// write, to find which one the private API still accepts.
func TestDiag_PointerVariants(t *testing.T) {
	dbID := os.Getenv("NOTION_TEST_DB_ID")
	if dbID == "" || os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2") == "" {
		t.Skip("env not set")
	}
	cp := testCtx(t, map[string]string{
		"parent_type": "database", "parent_id": dbID, "title": "wick diag ptr (hapus)",
	})
	rowOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	rowID := rowOut.(map[string]any)["id"].(string)
	cl, _ := newClient(testCtx(t, nil))
	idSpace, userID, _ := cl.identity()
	rm, _ := cl.syncRecordValues([]string{rowID})
	rec := recordValue(rm.Block[rowID])
	rowSpace := strField(rec, "space_id")
	collID := strField(rec, "parent_id")
	nameToID, idToType, _ := cl.fetchCollectionSchema(collID)
	pid := nameToID["Activity"]
	val, _ := formatProperty(idToType[pid], "Debug")
	var valAny any
	_ = json.Unmarshal(val, &valAny)
	t.Logf("row=%s coll=%s idSpace=%s rowSpace=%s pid=%q", rowID, collID, idSpace, rowSpace, pid)

	post := func(label string, body map[string]any) {
		if _, err := cl.post("/saveTransactions", body); err != nil {
			t.Logf("FAIL  %-46s %v", label, err)
			return
		}
		t.Logf("OK    %-46s", label)
	}
	tx := func(txSpace string, ops ...map[string]any) map[string]any {
		return map[string]any{
			"requestId": newUUID(),
			"transactions": []any{map[string]any{
				"id": newUUID(), "spaceId": txSpace, "operations": ops,
			}},
		}
	}
	ptr := func(extra map[string]any) map[string]any {
		p := map[string]any{"table": "block", "id": rowID, "spaceId": idSpace}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	mk := func(pointer map[string]any) map[string]any {
		return map[string]any{"pointer": pointer, "path": []any{"properties", pid},
			"command": "set", "args": valAny}
	}

	post("1 tx=idSpace ptr=idSpace", tx(idSpace, mk(ptr(nil))))
	post("2 tx=idSpace ptr=rowSpace", tx(idSpace, mk(ptr(map[string]any{"spaceId": rowSpace}))))
	post("3 tx=rowSpace ptr=idSpace", tx(rowSpace, mk(ptr(nil))))
	post("4 ptr+collectionId", tx(idSpace, mk(ptr(map[string]any{"collectionId": collID}))))
	post("5 ptr no spaceId", tx(idSpace, map[string]any{
		"pointer": map[string]any{"table": "block", "id": rowID},
		"path":    []any{"properties", pid}, "command": "set", "args": valAny}))
	post("6 args as raw string", tx(idSpace, map[string]any{
		"pointer": ptr(nil), "path": []any{"properties", pid},
		"command": "set", "args": "Debug"}))
	post("7 tx without spaceId", map[string]any{
		"requestId": newUUID(),
		"transactions": []any{map[string]any{
			"id": newUUID(), "operations": []map[string]any{mk(ptr(nil))}}}})

	_ = userID
	_ = connector.Ctx{}
}
