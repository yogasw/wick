import { describe, it, expect } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import TokenTrend from "../TokenTrend.svelte";
import type { TrendPoint } from "../usageReport.js";

const t = (total: number, cost = 0) => ({
  input: total / 10,
  cache_read: (total * 8) / 10,
  cache_write: 0,
  output: total / 10,
  total,
  cost_usd: cost,
  cache_hit_pct: 80,
});

// Four busy days with the per-conversation spend halving in the second
// half, and one quiet day that must not count as "zero per conversation".
const POINTS: TrendPoint[] = [
  { key: "2026-03-01", totals: t(1_000_000, 2), turns: 10, sessions: 2 },
  { key: "2026-03-02", totals: t(1_000_000, 2), turns: 10, sessions: 2 },
  { key: "2026-03-03", totals: t(0), turns: 0, sessions: 0 },
  { key: "2026-03-04", totals: t(500_000, 1), turns: 10, sessions: 2 },
  { key: "2026-03-05", totals: t(500_000, 1), turns: 10, sessions: 2 },
];

describe("TokenTrend", () => {
  it("opens per conversation and reports which way the range went", () => {
    render(TokenTrend, { props: { points: POINTS, unit: "day" } });
    const readout = screen.getByTestId("token-trend-readout");
    // 3M tokens over 8 conversation-buckets.
    expect(readout.textContent).toContain("375k");
    expect(screen.getByTestId("token-trend-change").textContent).toMatch(/▼\s*50%/);
  });

  it("switches measure and scale, and disables scales that are not questions", async () => {
    render(TokenTrend, { props: { points: POINTS, unit: "day" } });
    await fireEvent.click(screen.getByText("Conversations"));
    expect((screen.getByText("Per turn") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByTestId("token-trend-readout").textContent).toContain("8");
    await fireEvent.click(screen.getByText("Cost"));
    await fireEvent.click(screen.getByText("Per turn"));
    expect(screen.getByTestId("token-trend-readout").textContent).toContain("$0.15");
  });

  it("names the hovered bucket's breakdown", async () => {
    const { container } = render(TokenTrend, { props: { points: POINTS, unit: "day" } });
    const svg = container.querySelector("svg")!;
    svg.getBoundingClientRect = () => ({ left: 0, width: 500, top: 0, height: 150, right: 500, bottom: 150, x: 0, y: 0, toJSON() {} });
    await fireEvent.mouseMove(svg, { clientX: 10 });
    const text = screen.getByTestId("token-trend-readout").textContent ?? "";
    expect(text).toContain("2026-03-01");
    expect(text).toContain("2 conversations");
    expect(text).toContain("10 turns");
    expect(text).toContain("500k tokens / conversation");
  });
});
