package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
)

// usageFixture writes a session plus a ledger with two providers, the
// second one more recent — so "active" has to be chosen by time, not by
// map order.
func usageFixture(t *testing.T, id string) agentconfig.Layout {
	t.Helper()
	layout := fixtureSession(t, id)
	now := time.Now().UTC()

	su := store.SessionUsage{
		SessionID: id,
		Turns:     7,
		Totals:    store.UsageTotals{Input: 1000, Output: 250, CacheRead: 40, CostUSD: 0.5},
		UpdatedAt: now,
		Providers: map[string]*store.ProviderUsage{
			"codex/work": {
				UsageTotals: store.UsageTotals{Input: 200, Output: 50},
				Turns:       2,
				Model:       "gpt-x",
				LastAt:      now.Add(-time.Hour),
				// No window reported — pct must stay 0 rather than invent one.
				ContextUsed: 9000,
			},
			"claude/main": {
				UsageTotals: store.UsageTotals{Input: 800, Output: 200, CacheRead: 40, CostUSD: 0.5},
				Turns:       5,
				Model:       "claude-opus-5",
				ContextUsed: 50000, ContextWindow: 200000,
				LastAt: now,
				Series: []store.UsagePoint{{ContextUsed: 10}, {ContextUsed: 20}, {ContextUsed: 50000}},
			},
		},
	}
	b, err := json.Marshal(su)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}
	if err := os.WriteFile(layout.SessionUsage(id), b, 0o644); err != nil {
		t.Fatalf("write usage: %v", err)
	}
	return layout
}

func callContextTool(t *testing.T, layout agentconfig.Layout, sessionOnCall string, args map[string]any) ToolCallResult {
	t.Helper()
	r := httptest.NewRequest("POST", "/mcp", nil)
	if sessionOnCall != "" {
		r = r.WithContext(WithSessionID(r.Context(), sessionOnCall))
	}
	var got ToolCallResult
	WickContext(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, args)
	return got
}

func TestWickContext_ReadsTheCallsOwnSession(t *testing.T) {
	const id = "sess-abc"
	layout := usageFixture(t, id)

	got := callContextTool(t, layout, id, map[string]any{})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["session_id"] != id {
		t.Fatalf("session_id = %v", out["session_id"])
	}
	// The provider that answered MOST RECENTLY is the active one.
	if out["provider"] != "claude/main" {
		t.Fatalf("provider = %v, want the most recent one", out["provider"])
	}
	if out["used"] != float64(50000) || out["window"] != float64(200000) {
		t.Fatalf("used/window = %v/%v", out["used"], out["window"])
	}
	if out["pct"] != 25.0 {
		t.Fatalf("pct = %v, want 25", out["pct"])
	}
	if out["turns"] != float64(7) {
		t.Fatalf("turns = %v", out["turns"])
	}
	if trend, ok := out["trend"].([]any); !ok || len(trend) != 3 {
		t.Fatalf("trend = %v, want the recent per-turn levels", out["trend"])
	}
	if rows, ok := out["providers"].([]any); !ok || len(rows) != 2 {
		t.Fatalf("providers = %v, want a row each", out["providers"])
	}
}

// A window nobody reported must not be turned into a percentage.
func TestWickContext_NoWindowMeansNoPercentage(t *testing.T) {
	if got := pctOfInt(9000, 0); got != 0 {
		t.Fatalf("pct = %v, want 0 when the window is unknown", got)
	}
	if got := pctOfInt(50000, 200000); got != 25 {
		t.Fatalf("pct = %v, want 25", got)
	}
}

func TestWickUsage_ReportsFlows(t *testing.T) {
	const id = "sess-abc"
	layout := usageFixture(t, id)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), id))
	var got ToolCallResult
	WickUsage(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, map[string]any{})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// 1000 in + 40 cache read + 250 out
	if out["tokens"] != float64(1290) {
		t.Fatalf("tokens = %v, want 1290", out["tokens"])
	}
	totals, _ := out["totals"].(map[string]any)
	if totals["cost_usd"] != 0.5 {
		t.Fatalf("cost = %v", totals["cost_usd"])
	}
}

// A session with no ledger yet must answer plainly, not error.
func TestWickUsage_EmptyLedger(t *testing.T) {
	const id = "sess-fresh"
	layout := fixtureSession(t, id)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), id))
	var got ToolCallResult
	WickUsage(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, map[string]any{})
	if got.IsError {
		t.Fatalf("a session that has not run should not be an error: %+v", got)
	}
	if want := "nothing recorded yet"; !strings.Contains(got.Content[0].Text, want) {
		t.Fatalf("expected a plain note, got %s", got.Content[0].Text)
	}
}

func TestWickCompact_QueuesIntoTheCallsOwnSession(t *testing.T) {
	const id = "sess-abc"
	layout := usageFixture(t, id)
	// The session's agent runs claude, which can act on /compact.
	if err := session.AddAgent(layout, id, "main", "claude/main"); err != nil {
		t.Fatalf("add agent: %v", err)
	}

	var sentTo, sentText, sentRole string
	send := func(_ context.Context, sessionID, _, _, role, text string) error {
		sentTo, sentRole, sentText = sessionID, role, text
		return nil
	}

	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), id))
	var got ToolCallResult
	WickCompact(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, send, map[string]any{})
	if got.IsError {
		t.Fatalf("unexpected error: %+v", got)
	}
	if sentTo != id || sentText != "/compact" || sentRole != "user" {
		t.Fatalf("delivered %q as %q to %q", sentText, sentRole, sentTo)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(got.Content[0].Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The contract is "queued", not "done": nothing was compacted here.
	if out["status"] != "queued" {
		t.Fatalf("status = %v, want queued", out["status"])
	}
}

// No pool (stdio, tests) must say so rather than report a queued compaction
// that nobody will run.
func TestWickCompact_WithoutAPoolIsRefused(t *testing.T) {
	const id = "sess-abc"
	layout := usageFixture(t, id)

	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), id))
	var got ToolCallResult
	WickCompact(httptest.NewRecorder(), r, RPCRequest{}, captureResponder(t, &got), layout, nil, map[string]any{})
	if !got.IsError {
		t.Fatal("expected an error when no pool is wired")
	}
}
