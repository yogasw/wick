package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/mcp/handlers"
)

// A wick_execute result over handlers.MaxToolResultBytes comes back as a
// readable tool error instead of a message big enough to close the client's
// connection — on the JSON path (single and batch) and the SSE path — and
// the handler keeps serving normal calls afterwards.
func TestWickExecuteOversizedResultIsToolError(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)
	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list connectors: %v (rows=%d)", err, len(rows))
	}
	toolID := fmt.Sprintf("conn:%s/echo", rows[0].ID)
	big := strings.Repeat("x", handlers.MaxToolResultBytes+1024)

	type result struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	parse := func(raw []byte) result {
		t.Helper()
		var r result
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return r
	}
	assertTooLarge := func(label string, raw []byte) {
		t.Helper()
		if len(raw) > 64<<10 {
			t.Fatalf("%s: response is %d bytes, the oversized result was sent", label, len(raw))
		}
		r := parse(raw)
		if !r.Result.IsError || len(r.Result.Content) == 0 || !strings.Contains(r.Result.Content[0].Text, "result too large") {
			t.Fatalf("%s: want a 'result too large' tool error, got %s", label, raw)
		}
	}

	// Requests this big exceed the stdio line limit, so drive the HTTP
	// JSON path directly.
	call := func(args map[string]any) []byte {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"name": "wick_execute", "arguments": args})
		rec := httptest.NewRecorder()
		h.handleToolsCall(rec, httptest.NewRequest("POST", "/mcp", nil).WithContext(localAdminCtx()), rpcRequest{
			ID: json.RawMessage("1"), Method: "tools/call", Params: params,
		})
		return rec.Body.Bytes()
	}

	single := call(map[string]any{"tool_id": toolID, "params": map[string]any{"msg": big}})
	assertTooLarge("single", single)

	// Batch: an entry over the limit becomes an error entry and the rest
	// stay intact; when the entries fit one by one but not together, the
	// largest results are left out while every ok/error flag stays.
	type entry struct {
		Index         int             `json:"index"`
		OK            bool            `json:"ok"`
		Result        json.RawMessage `json:"result"`
		Error         string          `json:"error"`
		ResultOmitted string          `json:"result_omitted"`
	}
	type batchOut struct {
		Results []entry `json:"results"`
		OKCount int     `json:"ok_count"`
	}
	runBatchCall := func(msgs ...string) (batchOut, []byte) {
		t.Helper()
		calls := make([]any, len(msgs))
		for i, m := range msgs {
			calls[i] = map[string]any{"tool_id": toolID, "params": map[string]any{"msg": m}}
		}
		raw := call(map[string]any{"calls": calls})
		r := parse(raw)
		if r.Result.IsError {
			t.Fatalf("batch must not fail as a whole: %.300s", raw)
		}
		var b batchOut
		if err := json.Unmarshal([]byte(r.Result.Content[0].Text), &b); err != nil {
			t.Fatalf("batch body: %v", err)
		}
		return b, raw
	}

	b, raw := runBatchCall("write-done", big, "small")
	if len(raw) > handlers.MaxToolResultBytes*2 {
		t.Fatalf("batch response is %d bytes", len(raw))
	}
	if !b.Results[0].OK || !strings.Contains(string(b.Results[0].Result), "write-done") {
		t.Fatalf("entry 0 lost: %+v", b.Results[0])
	}
	if b.Results[1].OK || !strings.Contains(b.Results[1].Error, "result too large") || len(b.Results[1].Result) != 0 {
		t.Fatalf("entry 1 should be a too-large error: ok=%v err=%q", b.Results[1].OK, b.Results[1].Error)
	}
	if !b.Results[2].OK || !strings.Contains(string(b.Results[2].Result), "small") {
		t.Fatalf("entry 2 lost: %+v", b.Results[2])
	}

	// 1 write + 3 inline results of ~2 MB: together over 6 MB.
	two := strings.Repeat("z", 2<<20+4096)
	b, raw = runBatchCall("write-done", two, two, two)
	if len(raw) > handlers.MaxToolResultBytes+(1<<20) {
		t.Fatalf("batch response is %d bytes, over the limit", len(raw))
	}
	if !b.Results[0].OK || !strings.Contains(string(b.Results[0].Result), "write-done") {
		t.Fatalf("the write entry must stay ok with its result: %+v", b.Results[0])
	}
	if b.OKCount != 4 {
		t.Fatalf("ok_count = %d, want 4 (dropping a result does not fail the call)", b.OKCount)
	}
	omitted := 0
	for _, e := range b.Results[1:] {
		if !e.OK {
			t.Fatalf("entry %d flipped to failed", e.Index)
		}
		if e.ResultOmitted != "" {
			omitted++
			if len(e.Result) != 0 {
				t.Fatalf("entry %d omitted but still carries a result", e.Index)
			}
		}
	}
	if omitted == 0 || omitted == 3 {
		t.Fatalf("want some but not all 2 MB results left out, got %d", omitted)
	}

	// SSE single-call path.
	argsJSON, _ := json.Marshal(map[string]any{"tool_id": toolID, "params": map[string]any{"msg": big}})
	rec := httptest.NewRecorder()
	h.handleToolsCallSSE(rec, httptest.NewRequest("POST", "/mcp", nil).WithContext(localAdminCtx()), rpcRequest{
		ID:     json.RawMessage("1"),
		Method: "tools/call",
		Params: json.RawMessage(fmt.Sprintf(`{"name":"wick_execute","arguments":%s}`, argsJSON)),
	})
	sse := rec.Body.String()
	if len(sse) > 64<<10 || !strings.Contains(sse, "result too large") || !strings.Contains(sse, `"isError":true`) {
		t.Fatalf("sse: want a 'result too large' tool error frame, got %d bytes", len(sse))
	}

	// A normal call right after still works.
	ok := parse(call(map[string]any{"tool_id": toolID, "params": map[string]any{"msg": "hello"}}))
	if ok.Result.IsError || !strings.Contains(ok.Result.Content[0].Text, "hello") {
		t.Fatalf("small call after the oversized ones failed: %+v", ok)
	}
}

func TestOversizedResultBoundary(t *testing.T) {
	if msg := handlers.OversizedResult(strings.Repeat("a", handlers.MaxToolResultBytes)); msg != "" {
		t.Fatalf("exactly at the limit must pass, got %q", msg)
	}
	if msg := handlers.OversizedResult(strings.Repeat("a", handlers.MaxToolResultBytes+1)); !strings.Contains(msg, "result too large") {
		t.Fatalf("one byte over must fail, got %q", msg)
	}
	// Measured as it travels: 4 MB of quotes is 8 MB once JSON-escaped.
	if msg := handlers.OversizedResult(strings.Repeat(`"`, 4<<20)); !strings.Contains(msg, "result too large") {
		t.Fatalf("escaping must count, got %q", msg)
	}
}
