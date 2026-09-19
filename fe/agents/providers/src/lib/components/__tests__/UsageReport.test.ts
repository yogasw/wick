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

const PROVIDER_DETAIL = {
  provider: "claude/enginer",
  totals: {
    input: 1_000,
    cache_read: 9_000,
    cache_write: 0,
    output: 500,
    total: 10_500,
    cost_usd: 0.51,
    cache_hit_pct: 90,
  },
  sessions: Array.from({ length: 23 }, (_, i) => `sess${String(i).padStart(4, "0")}-rest-of-uuid`),
};

describe("UsageReport scoped to one provider", () => {
  it("shows that provider's own numbers, not the fleet's", async () => {
    mockFetch(PROVIDER_DETAIL);
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer" } });
    await waitFor(() => expect(screen.getByText("$0.51")).toBeTruthy());
    expect(screen.getByText(/claude\/enginer/)).toBeTruthy();
  });

  it("pages through the session list instead of rendering all of it", async () => {
    mockFetch(PROVIDER_DETAIL);
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer" } });
    await waitFor(() => expect(screen.getByText("Used in")).toBeTruthy());

    // 23 sessions at 10 per page = 3 pages, first page shows 10 rows.
    expect(screen.getByText("1 / 3")).toBeTruthy();
    expect(screen.getByText("sess0000")).toBeTruthy();
    expect(screen.queryByText("sess0010")).toBeNull();

    screen.getByText("→").click();
    await waitFor(() => expect(screen.getByText("2 / 3")).toBeTruthy());
    expect(screen.getByText("sess0010")).toBeTruthy();
  });

  it("says plainly when a provider has never been used", async () => {
    mockFetch({ ...PROVIDER_DETAIL, sessions: [] });
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer" } });
    await waitFor(() =>
      expect(screen.getByText(/No session has spent tokens on this provider/i)).toBeTruthy(),
    );
  });
});
