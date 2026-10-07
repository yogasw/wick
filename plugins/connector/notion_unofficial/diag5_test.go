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
	"testing"
)

// TestDiag_SpaceHeader checks whether the missing x-notion-space-id request header
// is what makes collection-property writes 400 while title writes pass.
func TestDiag_SpaceHeader(t *testing.T) {
	dbID := os.Getenv("NOTION_TEST_DB_ID")
	tok := os.Getenv("NOTION_UNOFFICIAL_TOKEN_V2")
	if dbID == "" || tok == "" {
		t.Skip("env not set")
	}
	cp := testCtx(t, map[string]string{"parent_type": "database", "parent_id": dbID, "title": "wick diag hdr (hapus)"})
	rowOut, err := createPage(cp)
	if err != nil {
		t.Fatalf("createPage: %v", err)
	}
	rowID := rowOut.(map[string]any)["id"].(string)

	cl, _ := newClient(testCtx(t, nil))
	idSpace, _, _ := cl.identity()
	rm, _ := cl.syncRecordValues([]string{rowID})
	collID := strField(recordValue(rm.Block[rowID]), "parent_id")
	rowSpace := strField(recordValue(rm.Block[rowID]), "space_id")
	nameToID, idToType, _ := cl.fetchCollectionSchema(collID)
	pid := nameToID["Activity"]
	val, _ := formatProperty(idToType[pid], "Debug")
	var v any
	_ = json.Unmarshal(val, &v)

	send := func(label, spaceHdr, txSpace string) {
		body := map[string]any{
			"requestId": newUUID(),
			"transactions": []any{map[string]any{
				"id": newUUID(), "spaceId": txSpace,
				"operations": []map[string]any{{
					"pointer": map[string]any{"table": "block", "id": rowID, "spaceId": txSpace},
					"path":    []any{"properties", pid}, "command": "set", "args": v,
				}},
			}},
		}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, "https://www.notion.so/api/v3/saveTransactions", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("cookie", "token_v2="+tok)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		if spaceHdr != "" {
			req.Header.Set("x-notion-space-id", spaceHdr)
		}
		if u := os.Getenv("NOTION_UNOFFICIAL_ACTIVE_USER_ID"); u != "" {
			req.Header.Set("x-notion-active-user-header", u)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("ERR   %-34s %v", label, err)
			return
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		st := "OK  "
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			st = "FAIL"
		}
		t.Logf("%s  %-34s status=%d body=%s", st, label, resp.StatusCode, string(raw))
	}

	send("no header (current behaviour)", "", idSpace)
	_ = rowSpace
}
