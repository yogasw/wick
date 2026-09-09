import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import UsageRings from "../UsageRings.svelte";
import { ringDash, pickWindows, connectionKey, resetHint } from "$lib/usagerings.js";

describe("pickWindows", () => {
  it("maps the 5-hour window to the inner ring and the 7-day to the outer", () => {
    const got = pickWindows([
      { key: "seven_day", utilization: 80, resetsAt: "" },
      { key: "five_hour", utilization: 25, resetsAt: "" },
    ]);
    expect(got.inner?.utilization).toBe(25);
    expect(got.outer?.utilization).toBe(80);
  });

  it("treats seven_day_opus and seven_day_fable as the weekly ring when seven_day is absent", () => {
    const opus = pickWindows([{ key: "seven_day_opus", utilization: 60, resetsAt: "" }]);
    expect(opus.outer?.utilization).toBe(60);
    const fable = pickWindows([{ key: "seven_day_fable", utilization: 61, resetsAt: "" }]);
    expect(fable.outer?.utilization).toBe(61);
  });

  it("prefers the plain seven_day window over a model-specific one", () => {
    const got = pickWindows([
      { key: "seven_day_opus", utilization: 90, resetsAt: "" },
      { key: "seven_day", utilization: 30, resetsAt: "" },
    ]);
    expect(got.outer?.utilization).toBe(30);
  });

  it("returns nulls for an empty window list", () => {
    const got = pickWindows([]);
    expect(got.inner).toBeNull();
    expect(got.outer).toBeNull();
  });
});

describe("ringDash", () => {
  it("renders a full circumference at 100%", () => {
    const { dash, circumference } = ringDash(100, 10);
    expect(dash).toBeCloseTo(circumference, 5);
  });

  it("renders nothing at 0%", () => {
    expect(ringDash(0, 10).dash).toBe(0);
  });

  it("clamps out-of-range utilization into 0-100", () => {
    const r = 10;
    const { circumference } = ringDash(0, r);
    expect(ringDash(140, r).dash).toBeCloseTo(circumference, 5);
    expect(ringDash(-20, r).dash).toBe(0);
  });
});

describe("UsageRings", () => {
  const windows = [
    { key: "five_hour", utilization: 42, resetsAt: "" },
    { key: "seven_day", utilization: 80, resetsAt: "" },
  ];

  it("shows both percentages in its accessible label", () => {
    render(UsageRings, { props: { windows } });
    const label = screen.getByRole("img").getAttribute("aria-label") ?? "";
    expect(label).toContain("42%");
    expect(label).toContain("80%");
  });

  it("draws one arc per present window", () => {
    const { container } = render(UsageRings, { props: { windows } });
    expect(container.querySelectorAll("[data-ring]")).toHaveLength(2);
  });

  it("draws only the ring it has data for", () => {
    const { container } = render(UsageRings, {
      props: { windows: [{ key: "five_hour", utilization: 5, resetsAt: "" }] },
    });
    expect(container.querySelectorAll("[data-ring]")).toHaveLength(1);
  });

  it("renders nothing when there are no windows", () => {
    const { container } = render(UsageRings, { props: { windows: [] } });
    expect(container.querySelector("svg")).toBeNull();
  });
});

describe("connectionKey", () => {
  it("keys on type and name so a card can join its connection row", () => {
    expect(connectionKey("claude", "enginer")).toBe("claude/enginer");
  });

  it("distinguishes instances that differ only by name", () => {
    expect(connectionKey("claude", "a")).not.toBe(connectionKey("claude", "b"));
  });
});

describe("resetHint", () => {
  const now = Date.parse("2026-09-07T09:00:00Z");

  it("renders the countdown in the largest single unit", () => {
    expect(resetHint({ key: "five_hour", utilization: 10, resetsAt: "2026-09-07T09:25:00Z" }, now).short).toBe("25m");
    expect(resetHint({ key: "five_hour", utilization: 10, resetsAt: "2026-09-07T12:10:00Z" }, now).short).toBe("3h");
    expect(resetHint({ key: "seven_day", utilization: 10, resetsAt: "2026-09-11T09:30:00Z" }, now).short).toBe("4d");
  });

  it("names the window in the tooltip, so the chip is not just a bare duration", () => {
    const got = resetHint({ key: "seven_day", utilization: 10, resetsAt: "2026-09-11T09:30:00Z" }, now);
    expect(got.full).toBe("Weekly (7 day) resets in 4d");
  });

  it("uses the model-specific weekly label when that is the reported window", () => {
    const got = resetHint({ key: "seven_day_opus", utilization: 10, resetsAt: "2026-09-08T09:00:00Z" }, now);
    expect(got.full).toBe("Weekly Fable resets in 1d");
  });

  // The card drops the element when both are empty, so a missing or stale
  // timestamp must not yield a stray separator.
  it("is empty for a null window, and for missing, past or unparseable timestamps", () => {
    expect(resetHint(null, now)).toEqual({ short: "", full: "" });
    for (const resetsAt of ["", "2026-09-07T08:00:00Z", "not-a-date"]) {
      expect(resetHint({ key: "five_hour", utilization: 10, resetsAt }, now)).toEqual({ short: "", full: "" });
    }
  });
});
