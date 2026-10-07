import { describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import UsagePopover from "../UsagePopover.svelte";
import type { ComposerUsage } from "../../api/usage.js";

function usage(over: Partial<ComposerUsage> = {}): ComposerUsage {
  return {
    provider: "claude/opus",
    supported: true,
    reason: "",
    account: {
      connected: true,
      email: "dev@abc.com",
      plan: "max",
      org: "",
      authMethod: "ChatGPT",
      expiresAt: "",
    },
    windows: [
      { key: "five_hour", utilization: 12, resetsAt: "", observedAt: "" },
      { key: "seven_day", utilization: 84, resetsAt: "", observedAt: "" },
    ],
    error: "",
    pending: false,
    checking: false,
    fetchedAt: new Date().toISOString(),
    ageS: 1,
    nextS: 0,
    canManage: false,
    accounts: [],
    rotation: "",
    ...over,
  };
}

function acct(over: Partial<ComposerUsage["accounts"][number]> = {}): ComposerUsage["accounts"][number] {
  return {
    id: "openai-codex#1", label: "a@example.test", provider: "openai-codex", email: "a@example.test",
    plan: "plus", status: "active", error: "", noUsage: false, current: false,
    windows: [{ key: "five_hour", utilization: 91, resetsAt: new Date(Date.now() + 2 * 3600e3).toISOString(), observedAt: "" }],
    ...over,
  };
}

const base = {
  open: true,
  loading: false,
  error: "",
  onRecheck: () => {},
  rechecking: false,
  recheckWait: 0,
  onClose: () => {},
};

describe("UsagePopover", () => {
  it("shows a window's utilization", () => {
    render(UsagePopover, { props: { ...base, data: usage() } });
    expect(screen.getByText("Weekly (7 day)")).toBeTruthy();
    expect(screen.getByText("84%")).toBeTruthy();
  });

  it("dates a figure the provider observed earlier", () => {
    // Codex publishes its limits only while it runs, so a reading can
    // be hours old. "last check just now" would describe when wick
    // looked at the file, which is not the same claim at all.
    const twoHoursAgo = new Date(Date.now() - 2 * 3600 * 1000).toISOString();
    render(UsagePopover, {
      props: {
        ...base,
        data: usage({
          provider: "codex/default",
          windows: [{ key: "seven_day", utilization: 84, resetsAt: "", observedAt: twoHoursAgo }],
        }),
      },
    });
    expect(screen.getByTestId("observed-at").textContent).toMatch(/as of 2h ago/);
  });

  it("does not date a live reading", () => {
    // Claude answers a request made for this reading; stamping it would
    // be noise on every row.
    render(UsagePopover, { props: { ...base, data: usage() } });
    expect(screen.queryByTestId("observed-at")).toBeNull();
  });

  it("summarises every account on one line each, session account first", async () => {
    const data = usage({
      provider: "omp/yoga",
      accounts: [
        acct(),
        acct({ id: "openai-codex#2", label: "b@example.test", email: "b@example.test", current: true,
          windows: [{ key: "five_hour", utilization: 5, resetsAt: "", observedAt: "" }, { key: "seven_day", utilization: 40, resetsAt: "", observedAt: "" }] }),
        acct({ id: "anthropic#1", provider: "anthropic", email: "", label: "anthropic account 1", status: "disabled", windows: [] }),
      ],
    });
    render(UsagePopover, { props: { ...base, data } });
    const rows = screen.getAllByTestId("usage-account-row");
    expect(rows).toHaveLength(3);
    expect(rows[0].textContent).toContain("b@example.test");
    expect(rows[0].textContent).toContain("this session");
    expect(rows[1].textContent).toContain("91%");
    expect(rows[1].textContent).toMatch(/reset 2h/);
    expect(rows[2].textContent).toContain("disabled");
    // The single-account ACCOUNT/USAGE block gives way to the list.
    expect(screen.queryByText("Weekly (7 day)")).toBeNull();
  });

  it("switches to per-account bars in Detail", async () => {
    render(UsagePopover, { props: { ...base, data: usage({ accounts: [acct(), acct({ id: "x#2", email: "c@example.test", error: "login token expired" })] }) } });
    await fireEvent.click(screen.getByTestId("usage-view-detail"));
    const blocks = screen.getAllByTestId("usage-account-detail");
    expect(blocks).toHaveLength(2);
    expect(blocks[0].textContent).toContain("Session (5hr)");
    expect(blocks[1].textContent).toContain("usage unavailable: login token expired");
  });

  it("says who rotates when the session is on Auto", () => {
    render(UsagePopover, { props: { ...base, data: usage({ rotation: "omp", accounts: [acct(), acct({ id: "y#2" })] }) } });
    expect(screen.getByTestId("usage-rotation").textContent).toContain("omp picks the account");
  });
});
