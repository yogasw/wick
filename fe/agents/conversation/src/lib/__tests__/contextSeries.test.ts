import { afterEach, describe, expect, it, vi } from "vitest";
import { pointsFor, valuesOf, selectionSummary, type View } from "../contextSeries.js";
import { fetchSessionContext, type SessionContext } from "../api/context.js";

const TOTALS = { input: 0, cache_read: 0, cache_write: 0, output: 0, total: 0, cost_usd: 1, cache_hit_pct: 0 };

function data(over: Partial<SessionContext> = {}): SessionContext {
  return {
    session_id: "S",
    provider: "a",
    used: 0,
    window: 1000,
    pct: 0,
    turns: 5,
    totals: TOTALS,
    providers: [
      { provider: "a", used: 0, window: 1000, pct: 0, turns: 3, totals: TOTALS },
      {
        provider: "b", used: 0, window: 0, pct: 0, turns: 2, totals: TOTALS,
        trend: [7, 8], trend_turn: [1, 2], trend_sent: [70, 80], trend_cost: [0.07, 0.08],
        trend_at: ["2026-10-13T00:02:00Z", "2026-10-13T00:09:00Z"],
      },
    ],
    trend: [1, 2, 3, 4],
    trend_turn: [1, null, 2, 3],
    trend_sent: [10, null, 20, 30],
    trend_cost: [0.1, null, 0.2, 0.3],
    trend_at: ["2026-10-13T00:01:00Z", "2026-10-13T00:03:00Z", "2026-10-13T00:05:00Z", "2026-10-13T00:07:00Z"],
    ...over,
  };
}

describe("contextSeries", () => {
  it("keeps real turn numbers and leaves level-only points unnumbered", () => {
    const list = pointsFor(data(), false, "last");
    expect(list.map((q) => q.turn)).toEqual([1, null, 2, 3]);
    expect(list.map((q) => q.sent)).toEqual([10, null, 20, 30]);
  });

  it("merges providers in time order and numbers session turns back from the total", () => {
    const list = pointsFor(data(), true, "all");
    expect(list.map((q) => `${q.provider}${q.turn ?? "-"}`)).toEqual(["a1", "b1", "a-", "a2", "a3", "b2"]);
    expect(list.map((q) => q.sturn)).toEqual([1, 2, null, 3, 4, 5]);
  });

  it("plots per turn or a running total across the range", () => {
    const list = pointsFor(data(), false, "all");
    expect(valuesOf(list, "usage", "turn")).toEqual([10, 0, 20, 30]);
    expect(valuesOf(list, "usage", "total")).toEqual([10, 10, 30, 60]);
    expect(valuesOf(list, "context", "turn")).toEqual([1, 2, 3, 4]);
  });

  it("anchors dollars spent so far to the provider's total", () => {
    const list = pointsFor(data(), false, "all");
    expect(list[3].cumCost).toBeCloseTo(1);
    expect(list[0].cumCost).toBeCloseTo(0.5);
  });

  it("summarises a selection", () => {
    const list = pointsFor(data(), false, "all");
    const v: View = { list, vals: valuesOf(list, "usage", "turn"), metric: "usage", agg: "turn", merged: false, window: 1000 };
    const text = selectionSummary(v, { a: 0, b: 2 }).map((s) => s.text).join("");
    expect(text).toBe("turns 1–2 · 30 sent · $0.30 · peak turn 2 (20)");
  });
});

describe("fetchSessionContext", () => {
  afterEach(() => vi.unstubAllGlobals());
  function stub() {
    const f = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) });
    vi.stubGlobal("fetch", f);
    return f;
  }
  it("asks for the default window without a query", async () => {
    const f = stub();
    await fetchSessionContext("", "S1");
    expect(f.mock.calls[0][0]).toBe("/api/sessions/S1/context");
  });
  it("passes trend=all and providers=all", async () => {
    const f = stub();
    await fetchSessionContext("", "S1", { trend: "all", providers: "all" });
    expect(f.mock.calls[0][0]).toBe("/api/sessions/S1/context?trend=all&providers=all");
  });
});
