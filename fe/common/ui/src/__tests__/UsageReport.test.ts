import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import UsageReport from "../UsageReport.svelte";
import { compactTokens, formatCost } from "../usageReport.js";

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
  window: "all",
  window_label: "All time",
  windows: [
    { key: "today", label: "Today" },
    { key: "7d", label: "7 days" },
    { key: "all", label: "All time" },
  ],
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

const rowTotals = (cost: number) => ({
  input: 100,
  cache_read: 900,
  cache_write: 0,
  output: 50,
  total: 1_050,
  cost_usd: cost,
  cache_hit_pct: 90,
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
  turns: 23,
  window: "all",
  window_label: "All time",
  windows: [
    { key: "today", label: "Today" },
    { key: "7d", label: "7 days" },
    { key: "all", label: "All time" },
  ],
  sessions: Array.from({ length: 23 }, (_, i) => ({
    id: `sess${String(i).padStart(4, "0")}-rest-of-uuid`,
    label: i === 0 ? "Debug ZAP komen TikTok" : "",
    project_id: i === 0 ? "proj-1" : "",
    project_name: i === 0 ? "Support Ops" : "",
    user_id: i === 0 ? "user-1" : "",
    user_name: i === 0 ? "Yoga Setiawan" : "",
    last_at: "2026-09-21T00:00:00Z",
    turns: 3,
    totals: rowTotals(0.02),
  })),
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
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer", defaultRange: "all" } });
    await waitFor(() =>
      expect(screen.getByText(/No session has spent tokens on this provider/i)).toBeTruthy(),
    );
  });
});

describe("UsageReport ranges", () => {
  it("asks the server for the range the user picked", async () => {
    const fn = mockFetch(REPORT);
    render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());
    // Today by default: the fleet ledger walks every session on disk, and
    // the answer people open this for is "what is it costing now".
    expect(String(fn.mock.calls[0][0])).toContain("window=today");

    screen.getByTestId("usage-range-7d").click();
    await waitFor(() => expect(fn).toHaveBeenCalledTimes(2));
    expect(String(fn.mock.calls[1][0])).toContain("window=7d");
    // Not a refresh: a different range is a different question, and
    // busting the server's cache for it would walk every session again.
    expect(String(fn.mock.calls[1][0])).not.toContain("refresh=1");
  });

  it("renders the ranges the server offers, not a list of its own", async () => {
    mockFetch(REPORT);
    render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByTestId("usage-range-7d")).toBeTruthy());
    expect(screen.queryByTestId("usage-range-90d")).toBeNull();
  });

  /* A windowed figure is rebuilt from a capped per-turn trail. When it
     cannot reach back far enough the number is a floor, and a floor
     shown as a total is the one way this panel can mislead. */
  it("passes on the server's caveat when a range is only a floor", async () => {
    mockFetch({ ...REPORT, partial: true, note: "figures for this range are a floor" });
    render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByTestId("usage-partial-note")).toBeTruthy());
    expect(screen.getByTestId("usage-partial-note").textContent).toContain("floor");
  });

  it("stays quiet about the caveat when the range is exact", async () => {
    mockFetch(REPORT);
    render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());
    expect(screen.queryByTestId("usage-partial-note")).toBeNull();
  });
});

describe("UsageReport session rows", () => {
  /* "Used in: 77f88066" answered how many, and nothing anybody could
     act on. The row has to say whose conversation it was and what it
     spent there. */
  it("names the session, its owner and its project", async () => {
    mockFetch(PROVIDER_DETAIL);
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer" } });
    await waitFor(() => expect(screen.getByTestId("usage-sessions")).toBeTruthy());

    expect(screen.getByText("Debug ZAP komen TikTok")).toBeTruthy();
    expect(screen.getByText("Yoga Setiawan")).toBeTruthy();
    expect(screen.getByText("Support Ops")).toBeTruthy();
    // The id stays as the small print that identifies the row.
    expect(screen.getByText("sess0000")).toBeTruthy();
  });

  it("falls back to the id for a session with nothing named", async () => {
    mockFetch(PROVIDER_DETAIL);
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer" } });
    await waitFor(() => expect(screen.getByText("sess0001")).toBeTruthy());
  });

  it("says which range came up empty, so it does not read as broken", async () => {
    mockFetch({ ...PROVIDER_DETAIL, sessions: [], window: "today", window_label: "Today" });
    render(UsageReport, { props: { base: "/t", provider: "claude/enginer" } });
    // Default is today, and "none today" must not read as "none ever".
    await waitFor(() => expect(screen.getByText(/in this range/i)).toBeTruthy());
  });
});

describe("UsageReport driven by a page filter", () => {
  /* A page that already has a range filter must not grow a second one.
     Two controls over one number is how somebody ends up comparing a
     30-day chart against a 7-day figure and trusting the comparison. */
  it("hides its own chips and asks for the range it was given", async () => {
    const fn = mockFetch(REPORT);
    render(UsageReport, { props: { base: "/t", range: "30d" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());

    expect(screen.queryByTestId("usage-range-today")).toBeNull();
    expect(String(fn.mock.calls[0][0])).toContain("window=30d");
  });

  it("asks for explicit dates when the page has a custom range", async () => {
    const fn = mockFetch(REPORT);
    render(UsageReport, {
      props: { base: "/t", since: "2026-09-01", until: "2026-09-07" },
    });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());

    const url = String(fn.mock.calls[0][0]);
    expect(url).toContain("since=2026-09-01");
    expect(url).toContain("until=2026-09-07");
    // Dates win over a named window — asking for both would be ambiguous.
    expect(url).not.toContain("window=");
  });
});

/* "Which provider" stops one level short of the question people actually
   ask — a provider runs opus one turn and haiku the next, and which it
   ran is what decides the bill. */
describe("UsageReport — by model", () => {
  const WITH_MODELS = {
    ...REPORT,
    by_model: [
      {
        key: "claude/opus|claude-opus-5",
        label: "claude-opus-5",
        group: "claude/opus",
        totals: {
          input: 900, cache_read: 9_000, cache_write: 0, output: 400,
          total: 10_300, cost_usd: 1.2, cache_hit_pct: 91,
        },
        turns: 4,
        share: 98,
      },
      {
        key: "codex/gpt|gpt-5",
        label: "gpt-5",
        group: "codex/gpt",
        totals: {
          input: 100, cache_read: 0, cache_write: 0, output: 100,
          total: 200, cost_usd: 0, cache_hit_pct: 0,
        },
        turns: 1,
        share: 2,
      },
    ],
  };

  it("names the model, and which provider it came through", async () => {
    mockFetch(WITH_MODELS);
    const { getByText } = render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());

    getByText("By model").click();
    await waitFor(() => expect(screen.getByText("claude-opus-5")).toBeTruthy());
    expect(screen.getByText("gpt-5")).toBeTruthy();
    // The same model id through two accounts is two rows; the provider is
    // the only thing that tells them apart, so it rides along under the
    // model name.
    expect(screen.getAllByText("claude/opus").length).toBeGreaterThan(0);
  });

  it("shows turns, which is the only figure a flat-rate plan has", async () => {
    mockFetch(WITH_MODELS);
    const { getByText } = render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());

    getByText("By model").click();
    await waitFor(() => expect(screen.getByText("claude-opus-5")).toBeTruthy());
    expect(screen.getByText("4")).toBeTruthy();
  });

  // An older server sends no by_model at all. The tab has to say why it
  // is empty rather than look broken.
  it("explains an empty model breakdown", async () => {
    mockFetch(REPORT);
    const { getByText } = render(UsageReport, { props: { base: "/t" } });
    await waitFor(() => expect(screen.getByText("claude/opus")).toBeTruthy());

    getByText("By model").click();
    await waitFor(() =>
      expect(screen.getByText(/a turn records its model as it finishes/i)).toBeTruthy(),
    );
  });

  it("lists the models on one provider's own page", async () => {
    mockFetch({
      provider: "claude/opus",
      totals: REPORT.totals,
      turns: 5,
      by_model: WITH_MODELS.by_model.slice(0, 1),
      sessions: [],
      window: "all",
      window_label: "All time",
      windows: REPORT.windows,
    });
    render(UsageReport, { props: { base: "/t", provider: "claude/opus" } });
    await waitFor(() => expect(screen.getByTestId("provider-models")).toBeTruthy());
    expect(screen.getByTestId("provider-models").textContent).toContain("claude-opus-5");
  });
});
