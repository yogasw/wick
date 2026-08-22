import { describe, test, expect } from "vitest";
import {
  DEFAULT_VISIBLE,
  MAX_VISIBLE,
  MIN_VISIBLE,
  clampVisible,
  moveInOrder,
  orderTabs,
  parseRailPrefs,
  splitRail,
} from "../railPrefs.js";

const tabs = [
  { id: "ticket" },
  { id: "notes" },
  { id: "context" },
  { id: "process" },
  { id: "workspace" },
  { id: "scheduled" },
  { id: "browser" },
  { id: "source" },
];

const ids = (list: { id: string }[]) => list.map((t) => t.id);
const nothingLoud = () => false;

describe("clampVisible", () => {
  test("keeps a sane value", () => {
    expect(clampVisible(4)).toBe(4);
  });
  test("clamps out of range", () => {
    expect(clampVisible(0)).toBe(MIN_VISIBLE);
    expect(clampVisible(99)).toBe(MAX_VISIBLE);
  });
  // A profile written by an older client can hold anything.
  test("falls back on junk", () => {
    expect(clampVisible(undefined)).toBe(DEFAULT_VISIBLE);
    expect(clampVisible("many")).toBe(DEFAULT_VISIBLE);
    expect(clampVisible(NaN)).toBe(DEFAULT_VISIBLE);
  });
});

describe("parseRailPrefs", () => {
  test("empty input yields defaults", () => {
    expect(parseRailPrefs(undefined)).toEqual({ order: [], visible: DEFAULT_VISIBLE });
  });
  test("drops non-string ids", () => {
    const p = parseRailPrefs({ order: ["ticket", 5, null, "notes"], visible: 3 });
    expect(p.order).toEqual(["ticket", "notes"]);
    expect(p.visible).toBe(3);
  });
});

describe("orderTabs", () => {
  test("saved order leads, unlisted tabs follow in built-in order", () => {
    const got = orderTabs(tabs, ["source", "context"]);
    expect(ids(got).slice(0, 2)).toEqual(["source", "context"]);
    // The rest keep their original sequence rather than being shuffled.
    expect(ids(got).slice(2)).toEqual([
      "ticket",
      "notes",
      "process",
      "workspace",
      "scheduled",
      "browser",
    ]);
  });

  // A saved order outlives the tabs it names: Browser disappears without a
  // browser instance, and a later release adds new ones.
  test("ignores ids that no longer exist", () => {
    const got = orderTabs([{ id: "ticket" }, { id: "notes" }], ["browser", "notes"]);
    expect(ids(got)).toEqual(["notes", "ticket"]);
  });

  test("a tab added after the prefs were saved is not lost", () => {
    const got = orderTabs(tabs, ["ticket", "notes"]);
    expect(ids(got)).toContain("source");
  });
});

describe("splitRail", () => {
  test("everything shows when it fits", () => {
    const { shown, overflow } = splitRail(tabs.slice(0, 3), 4, nothingLoud);
    expect(ids(shown)).toEqual(["ticket", "notes", "context"]);
    expect(overflow).toEqual([]);
  });

  test("the first N show, the rest overflow", () => {
    const { shown, overflow } = splitRail(tabs, 3, nothingLoud);
    expect(ids(shown)).toEqual(["ticket", "notes", "context"]);
    expect(ids(overflow)).toEqual(["process", "workspace", "scheduled", "browser", "source"]);
  });

  // The point of promotion: a count nobody can see is worse than a tab in
  // an unexpected slot.
  test("a loud tab is promoted out of the overflow", () => {
    const { shown, overflow } = splitRail(tabs, 3, (id) => id === "source");
    expect(ids(shown)).toContain("source");
    expect(ids(overflow)).not.toContain("source");
    expect(shown).toHaveLength(3);
  });

  // Promotion changes WHICH tabs are visible, never their sequence — so a
  // remembered left-to-right reading still holds.
  test("promotion keeps the user's relative order", () => {
    const { shown } = splitRail(tabs, 3, (id) => id === "source" || id === "scheduled");
    // scheduled comes before source in the ordered list, and still does.
    expect(ids(shown)).toEqual(["ticket", "scheduled", "source"]);
  });

  test("more loud tabs than slots: the earliest ones win", () => {
    const { shown, overflow } = splitRail(tabs, 2, () => true);
    expect(ids(shown)).toEqual(["ticket", "notes"]);
    expect(ids(overflow)).toHaveLength(6);
  });

  test("quiet tabs fill the slots left over", () => {
    const { shown } = splitRail(tabs, 4, (id) => id === "source");
    // One promoted, three quiet from the front.
    expect(ids(shown)).toEqual(["ticket", "notes", "context", "source"]);
  });

  test("an out-of-range count is clamped rather than trusted", () => {
    const { shown } = splitRail(tabs, 99, nothingLoud);
    // Clamped to the ceiling, which is above the tab count here, so
    // everything fits.
    expect(shown).toHaveLength(tabs.length);
  });

  // The open panel's own tab must stay clickable: hiding it would leave a
  // panel with no way to close it.
  test("the active tab is kept in the strip", () => {
    const { shown, overflow } = splitRail(tabs, 3, nothingLoud, "source");
    expect(ids(shown)).toContain("source");
    expect(ids(overflow)).not.toContain("source");
  });

  test("the active tab keeps its place in the order", () => {
    const { shown } = splitRail(tabs, 3, nothingLoud, "scheduled");
    expect(ids(shown)).toEqual(["ticket", "notes", "scheduled"]);
  });
});

describe("moveInOrder", () => {
  test("swaps with the neighbour", () => {
    expect(moveInOrder(["a", "b", "c"], "b", -1)).toEqual(["b", "a", "c"]);
    expect(moveInOrder(["a", "b", "c"], "b", 1)).toEqual(["a", "c", "b"]);
  });
  test("edges are a no-op", () => {
    expect(moveInOrder(["a", "b"], "a", -1)).toEqual(["a", "b"]);
    expect(moveInOrder(["a", "b"], "b", 1)).toEqual(["a", "b"]);
  });
  test("an unknown id is a no-op", () => {
    expect(moveInOrder(["a", "b"], "zz", 1)).toEqual(["a", "b"]);
  });
  test("does not mutate the input", () => {
    const order = ["a", "b", "c"];
    moveInOrder(order, "a", 1);
    expect(order).toEqual(["a", "b", "c"]);
  });
});
