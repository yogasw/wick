// SOLVED 2026-09-28 — kept as the record of how, not as a live question.
// The answer is updateBlockPropertyValue (see propOp in repo.go and the
// recorded payload in testdata/captured-property-edit.json). Guessing command
// names, which is what several of these probes do, never found it; recording
// the web client's own request did. Every test here is env-gated and skips.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"testing"
)

var reDebug = regexp.MustCompile(`"debugMessage":"([^"]*)"`)

// TestDiag_HighLevelPropertyOps probes candidate "high-level property operations"
// until one is accepted, reading Notion's debugMessage for each attempt.
func TestDiag_HighLevelPropertyOps(t *testing.T) {
	dbID := os.Getenv("NOTION_TEST_DB_ID")
	tok := os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2")
	if dbID == "" || tok == "" {
		t.Skip("env not set")
	}
	cp := testCtx(t, map[string]string{"parent_type": "database", "parent_id": dbID, "title": "wick diag hl (hapus)"})
	rowOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	rowID := rowOut.(map[string]any)["id"].(string)
	t.Logf("row %s", rowID)

	cl, _ := newClient(testCtx(t, nil))
	space, _, _ := cl.identity()
	rm, _ := cl.syncRecordValues([]string{rowID})
	collID := strField(recordValue(rm.Block[rowID]), "parent_id")
	nameToID, idToType, _ := cl.fetchCollectionSchema(collID)
	pid := nameToID["Activity"]
	val, _ := formatProperty(idToType[pid], "Debug")
	var v any
	_ = json.Unmarshal(val, &v)

	ptr := map[string]any{"table": "block", "id": rowID, "spaceId": space}

	try := func(label string, o map[string]any) {
		body := map[string]any{"requestId": newUUID(),
			"transactions": []any{map[string]any{"id": newUUID(), "spaceId": space,
				"operations": []map[string]any{o}}}}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, "https://www.notion.so/api/v3/saveTransactions", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("cookie", "token_v2="+tok)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("ERR   %-30s %v", label, err)
			return
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			t.Logf("OK    %-30s <<< ACCEPTED", label)
			return
		}
		msg := ""
		if m := reDebug.FindStringSubmatch(string(raw)); m != nil {
			msg = m[1]
		}
		t.Logf("FAIL  %-30s %d %s", label, resp.StatusCode, msg)
	}

	// second round: more command names + arg shapes
	try("setProperty raw args", map[string]any{"pointer": ptr, "path": []any{pid},
		"command": "setProperty", "args": v})
	try("setProperty path[properties,id]", map[string]any{"pointer": ptr, "path": []any{"properties", pid},
		"command": "setProperty", "args": v})
	try("updatePropertyValue", map[string]any{"pointer": ptr, "path": []any{"properties", pid},
		"command": "updatePropertyValue", "args": v})
	try("setBlockPropertyValue", map[string]any{"pointer": ptr, "path": []any{"properties", pid},
		"command": "setBlockPropertyValue", "args": v})
	try("setProperties", map[string]any{"pointer": ptr, "path": []any{},
		"command": "setProperties", "args": map[string]any{pid: v}})
	try("updateProperties", map[string]any{"pointer": ptr, "path": []any{},
		"command": "updateProperties", "args": map[string]any{pid: v}})
	try("setPropertyOp", map[string]any{"pointer": ptr, "path": []any{"properties", pid},
		"command": "setPropertyOp", "args": v})
	try("propertySet", map[string]any{"pointer": ptr, "path": []any{"properties", pid},
		"command": "propertySet", "args": v})
}
