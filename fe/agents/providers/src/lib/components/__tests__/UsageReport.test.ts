import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import UsageReport from "../UsageReport.svelte";
import { compactTokens, formatCost } from "../../usageReport.js";

const REPORT = {
  totals: {
    input: 1_000,
    cache_read: 9_000,
    cache_write: 0,
    output: 500,
    total: 10_500,
    cost_usd: 1.25,
    cache_hit_pct: 90,
  },
  turns: 5,
  sessions: 2,
  by_provider: [
    {
      key: "claude/opus",
      totals: {
        input: 900,
        cache_read: 9_000,
        cache_write: 0,
        output: 400,
        total: 10_300,
        cost_usd: 1.2,
        cache_hit_pct: 91,
      },
      sessions: 2,
      share: 98,
    },
    {
      key: "codex/gpt",
      totals: {
        input: 100,
        cache_read: 0,
        cache_write: 0,
        output: 100,
        total: 200,
        cost_usd: 0,
        cache_hit_pct: 0,
      },
      sessions: 1,
      share: 2,
    },
  ],
  by_project: [],
  by_user: [],
};

function mockFetch(body: unknown, ok = true) {
  const fn = vi.fn().mockResolvedValue({
    ok,
    status: ok ? 200 : 500,
    json: async () => body,
  });
  vi.stubGlobal("fetch", fn);
  return fn;
}

afterEach(() => vi.unstubAllGlobals());

describe("UsageReport", () => {
  it("leads with cost and shows the provider breakdown", async () => {
    mockFetch(REPORT);
    render(UsageReport, { props: { base: "/t" } });

    await waitFor(() => expect(screen.getByText("$1.25")).toBeTruthy());
    expect(screen.getByText("claude/opus")).toBeTruthy();
    expect(screen.getByText("codex/gpt")).toBeTruthy();
    // A provider that reports no cost must read as unknown, not as free.
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  it("explains an empty slice instead of just showing nothing", async () => {
    mockFetch(REPORT);
    const { getByText } = render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());

    getByText("By project").click();
    await waitFor(() =>
      expect(screen.getByText(/sessions started outside a project/i)).toBeTruthy(),
    );
  });

  it("surfaces a load failure rather than rendering zeroes", async () => {
    mockFetch({}, false);
    render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText(/Couldn't load usage/i)).toBeTruthy());
  });

  it("does not poll — one fetch until the user asks for more", async () => {
    const fn = mockFetch(REPORT);
    render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("number formatting", () => {
  it("compacts token counts at each magnitude", () => {
    expect(compactTokens(0)).toBe("0");
    expect(compactTokens(999)).toBe("999");
    expect(compactTokens(1_500)).toBe("1.5k");
    expect(compactTokens(31_168)).toBe("31k");
    expect(compactTokens(1_234_567)).toBe("1.23M");
    expect(compactTokens(12_345_678)).toBe("12.3M");
  });

  it("never rounds a real cost down to $0.00", () => {
    expect(formatCost(0)).toBe("—");
    expect(formatCost(0.004)).toBe("<$0.01");
    expect(formatCost(1.2345)).toBe("$1.23");
    expect(formatCost(1234.5)).toBe("$1,235");
  });
});
