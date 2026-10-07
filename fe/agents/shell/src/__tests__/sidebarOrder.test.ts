import { describe, test, expect, vi, afterEach } from "vitest";
import { relativeAge, isRunningStatus, sortRows, touchesActivity } from "../sidebarOrder.js";
import { applyRowStatus, createResorter, refreshAges, resortRows } from "../sidebarRows.js";

/* relativeAge / sortRows mirror internal/tools/agents/view/sidebar_time_test.go
   and sidebar_order_test.go: the server paints the first frame, this code
   keeps it current, so the two must agree on every label and order. */

describe("relativeAge", () => {
  const now = 10_000_000_000;
  const cases: [number, string][] = [
    [0, "now"],
    [59_999, "now"],
    [60_000, "1m"],
    [3 * 60_000 + 59_000, "3m"],
    [3_599_999, "59m"],
    [3_600_000, "1h"],
    [2 * 3_600_000 + 1, "2h"],
    [86_400_000, "1d"],
    [5 * 86_400_000 + 7, "5d"],
    [-5_000, "now"],
  ];
  for (const [ago, want] of cases) {
    test(`${ago}ms ago → ${want}`, () => {
      expect(relativeAge(now - ago, now)).toBe(want);
    });
  }
  test("no recorded activity has no label", () => {
    expect(relativeAge(0, now)).toBe("");
  });
});

describe("isRunningStatus", () => {
  test("working, spawning, subagent and queued run; idle does not", () => {
    expect(["working", "spawning", "subagent", "queued"].every(isRunningStatus)).toBe(true);
    expect(["idle", "killed", ""].some(isRunningStatus)).toBe(false);
  });
});

describe("sortRows", () => {
  test("running first, then newest first, then id on ties", () => {
    const got = sortRows([
      { id: "old-idle", running: false, lastActive: 1 },
      { id: "new-idle", running: false, lastActive: 50 },
      { id: "old-run", running: true, lastActive: 2 },
      { id: "tie-a", running: false, lastActive: 10 },
      { id: "tie-b", running: false, lastActive: 10 },
      { id: "new-run", running: true, lastActive: 40 },
    ]).map((r) => r.id);
    expect(got).toEqual(["new-run", "old-run", "new-idle", "tie-a", "tie-b", "old-idle"]);
  });

  /* Equal-time rows order by id, as orderSidebarIDs does on the server —
     not by where they happened to sit — so the first live re-sort after
     load does not reshuffle rows the server already ordered. */
  test("equal-time rows follow id, whatever order they arrive in", () => {
    const got = sortRows([
      { id: "tie-b", running: false, lastActive: 10 },
      { id: "tie-a", running: false, lastActive: 10 },
    ]).map((r) => r.id);
    expect(got).toEqual(["tie-a", "tie-b"]);
  });
});

describe("touchesActivity", () => {
  test("starting or stopping work counts as use", () => {
    expect(touchesActivity(undefined, "working")).toBe(true);
    expect(touchesActivity("working", "idle")).toBe(true);
    expect(touchesActivity("subagent", "")).toBe(true);
  });
  test("a replayed warm idle process does not jump to now", () => {
    expect(touchesActivity(undefined, "idle")).toBe(false);
    expect(touchesActivity("idle", "idle")).toBe(false);
  });
});

function row(id: string, lastActive: number, running = false): string {
  return `<a data-session-row="${id}" data-last-active="${lastActive}" data-running="${running}"
    class="border-l-2 ${running ? "border-green-500" : "border-transparent"}">
    <span data-session-title="${id}">${id}</span>
    <span data-session-age="${id}" class="${running ? "hidden" : ""}"></span>
  </a>`;
}

function mount(html: string): HTMLElement {
  document.body.innerHTML = `<div data-sidebar-sessions>${html}</div>`;
  return document.querySelector<HTMLElement>("[data-sidebar-sessions]")!;
}

const order = (list: HTMLElement) =>
  Array.from(list.querySelectorAll<HTMLElement>("[data-session-row]")).map((r) => r.dataset["sessionRow"]);

describe("sidebar rows", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  test("refreshAges relabels every row", () => {
    const now = 1_000_000_000;
    const list = mount(row("a", now - 3 * 60_000) + row("b", now - 2 * 3_600_000));
    refreshAges(list, now);
    expect(list.querySelector('[data-session-age="a"]')!.textContent).toBe("3m");
    expect(list.querySelector('[data-session-age="b"]')!.textContent).toBe("2h");
  });

  test("a session that starts working gets the accent, hides its age, and is stamped now", () => {
    const now = 1_000_000_000;
    const list = mount(row("a", now - 3_600_000));
    const a = list.querySelector<HTMLElement>('[data-session-row="a"]')!;
    expect(applyRowStatus(a, undefined, "working", now)).toBe(true);
    expect(a.dataset["running"]).toBe("true");
    expect(a.classList.contains("border-green-500")).toBe(true);
    expect(a.classList.contains("border-transparent")).toBe(false);
    expect(a.classList.contains("bg-green-500/5")).toBe(true);
    expect(a.querySelector("[data-session-age]")!.classList.contains("hidden")).toBe(true);
    expect(a.dataset["lastActive"]).toBe(String(now));

    // Finishing shows the age again, reading "now".
    expect(applyRowStatus(a, "working", "idle", now + 1000)).toBe(true);
    expect(a.classList.contains("border-transparent")).toBe(true);
    expect(a.classList.contains("bg-green-500/5")).toBe(false);
    const age = a.querySelector<HTMLElement>("[data-session-age]")!;
    expect(age.classList.contains("hidden")).toBe(false);
    expect(age.textContent).toBe("now");
  });

  test("the open row gets the accent but not the wash", () => {
    const list = mount(row("a", 1));
    const a = list.querySelector<HTMLElement>('[data-session-row="a"]')!;
    a.setAttribute("aria-current", "page");
    applyRowStatus(a, undefined, "working", 1_000_000_000);
    expect(a.classList.contains("border-green-500")).toBe(true);
    expect(a.classList.contains("bg-green-500/5")).toBe(false);
  });

  test("a replayed idle row keeps its age and needs no re-sort", () => {
    const now = 1_000_000_000;
    const list = mount(row("a", now - 5 * 60_000));
    const a = list.querySelector<HTMLElement>('[data-session-row="a"]')!;
    expect(applyRowStatus(a, undefined, "idle", now)).toBe(false);
    expect(a.dataset["lastActive"]).toBe(String(now - 5 * 60_000));
  });

  test("resortRows moves running rows to the top, then newest first", () => {
    const list = mount(row("old", 1) + row("new", 50) + row("run", 2, true));
    resortRows(list);
    expect(order(list)).toEqual(["run", "new", "old"]);
  });

  test("the resorter throttles bursts and waits while the pointer is over the list", () => {
    vi.useFakeTimers();
    let t = 0;
    const list = mount(row("a", 10) + row("b", 5));
    const r = createResorter(list, { minIntervalMs: 3000, now: () => t });
    const b = list.querySelector<HTMLElement>('[data-session-row="b"]')!;

    b.dataset["lastActive"] = "20";
    r.request();
    expect(order(list)).toEqual(["b", "a"]); // first one runs at once

    const a = list.querySelector<HTMLElement>('[data-session-row="a"]')!;
    a.dataset["lastActive"] = "30";
    t = 1000;
    r.request();
    expect(order(list)).toEqual(["b", "a"]); // throttled
    t = 3000;
    vi.advanceTimersByTime(2000);
    expect(order(list)).toEqual(["a", "b"]);

    // Hovering holds the re-sort until the pointer leaves.
    list.dispatchEvent(new Event("pointerenter"));
    b.dataset["lastActive"] = "40";
    t = 10_000;
    r.request();
    vi.advanceTimersByTime(10_000);
    expect(order(list)).toEqual(["a", "b"]);
    list.dispatchEvent(new Event("pointerleave"));
    expect(order(list)).toEqual(["b", "a"]);
  });
});
