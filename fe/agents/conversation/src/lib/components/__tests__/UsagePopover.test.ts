import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
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
});
