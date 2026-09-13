// Self-test for ticket_search — the op that answers "where is the one about
// X", which no combination of status and assignee filters can.
package tickets

import (
	"encoding/json"
	"testing"

	"github.com/yogasw/wick/internal/agents/ticket"
)

var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
)

func mkTicket(t *testing.T, h *handlers, id, title, body string, fields map[string]string) {
	t.Helper()
	if _, err := ticket.Create(h.layout, ticket.CreateOptions{
		ID: id, ProjectID: "p1", Title: title, Body: body, Status: "open", Fields: fields,
	}); err != nil {
		t.Fatal(err)
	}
}

func searchHits(t *testing.T, h *handlers, input map[string]string) []map[string]any {
	t.Helper()
	res := mustDispatch(t, h.search, ctxFor("s1", input))
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result shape: %T", res)
	}
	raw, ok := m["tickets"]
	if !ok {
		t.Fatal("no tickets key")
	}
	out := []map[string]any{}
	switch v := raw.(type) {
	default:
		// The handler returns a typed slice; reach it through the JSON shape
		// the connector would emit rather than importing the private type.
		b := mustJSON(t, v)
		out = mustUnmarshalList(t, b)
	}
	return out
}

func TestSearchFindsByTitleBodyAndField(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	mkTicket(t, h, "t1", "Payment webhook returns 401", "", nil)
	mkTicket(t, h, "t2", "Nightly report is late", "the webhook that feeds it times out", nil)
	mkTicket(t, h, "t3", "Something else entirely", "", map[string]string{"component": "webhook-gateway"})
	mkTicket(t, h, "t4", "Unrelated", "nothing to see", nil)

	hits := searchHits(t, h, map[string]string{"query": "webhook"})
	if len(hits) != 3 {
		t.Fatalf("want 3 hits, got %d: %v", len(hits), hits)
	}
	// Title first: a ticket named after the thing you searched for is almost
	// always the one you meant.
	if hits[0]["id"] != "t1" {
		t.Fatalf("title hit did not rank first: %v", hits[0]["id"])
	}
	if mi, _ := hits[0]["matched_in"].([]any); len(mi) == 0 || mi[0] != "title" {
		t.Fatalf("matched_in wrong for the title hit: %v", hits[0]["matched_in"])
	}
}

func TestSearchIsCaseInsensitive(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	mkTicket(t, h, "t1", "Payment Webhook Returns 401", "", nil)

	if got := len(searchHits(t, h, map[string]string{"query": "payment webhook"})); got != 1 {
		t.Fatalf("case-insensitive search missed it: %d hits", got)
	}
}

func TestSearchNarrowsByStatus(t *testing.T) {
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	mkTicket(t, h, "t1", "webhook one", "", nil)
	tk, err := ticket.Load(l, "p1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	tk.Status = "done"
	if err := ticket.Save(l, tk); err != nil {
		t.Fatal(err)
	}
	mkTicket(t, h, "t2", "webhook two", "", nil)

	hits := searchHits(t, h, map[string]string{"query": "webhook", "status": "open"})
	if len(hits) != 1 || hits[0]["id"] != "t2" {
		t.Fatalf("status filter ignored: %v", hits)
	}
}

func TestSearchRequiresAQuery(t *testing.T) {
	// An empty query would return the whole board dressed up as a search
	// result, which is the one answer nobody asked for.
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	if _, err := h.search(ctxFor("s1", map[string]string{"query": "  "})); err == nil {
		t.Fatal("empty query was accepted")
	}
}

func TestSearchLimitStillReportsWhatWasScanned(t *testing.T) {
	// A truncated result that claims to be complete is worse than no result.
	h, l := newTestHandlers(t)
	mkSession(t, l, "s1", "p1")
	mkTicket(t, h, "t1", "webhook one", "", nil)
	mkTicket(t, h, "t2", "webhook two", "", nil)
	mkTicket(t, h, "t3", "webhook three", "", nil)

	res := mustDispatch(t, h.search, ctxFor("s1", map[string]string{"query": "webhook", "limit": "1"}))
	m := res.(map[string]any)
	if m["total"].(int) != 1 {
		t.Fatalf("limit ignored: %v", m["total"])
	}
	if m["scanned"].(int) != 3 {
		t.Fatalf("scanned should count every candidate, got %v", m["scanned"])
	}
}

/* ── helpers ──────────────────────────────────────────────────────────── */

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonMarshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustUnmarshalList(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := jsonUnmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
