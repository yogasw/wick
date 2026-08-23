import { describe, test, expect } from "vitest";
import {
  DEFAULT_HIDDEN,
  moveInOrder,
  reorderTo,
  orderTabs,
  parseRailPrefs,
  railPrefsFromPage,
  resolveHidden,
  splitRail,
  toggleHidden,
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

describe("parseRailPrefs", () => {
  // null, not []: "never arranged" takes the default, while an empty list is
  // a deliberate "show everything". Collapsing them would re-fold the tabs
  // someone had just unfolded.
  test("empty input leaves the layout unarranged", () => {
    expect(parseRailPrefs(undefined)).toEqual({ order: [], hidden: null });
  });

  test("an explicit empty list is a choice, not an absence", () => {
    expect(parseRailPrefs({ order: [], hidden: [] }).hidden).toEqual([]);
  });

  test("drops non-string ids from both lists", () => {
    const p = parseRailPrefs({ order: ["ticket", 5, null, "notes"], hidden: ["browser", 9] });
    expect(p.order).toEqual(["ticket", "notes"]);
    expect(p.hidden).toEqual(["browser"]);
  });

  // Layouts saved before folding became per-tab hold `visible: N`. Resetting
  // those would throw away a layout someone had already arranged, so the
  // count is read as "the first N stay, the rest are hidden".
  test("migrates an older visible-count layout", () => {
    const p = parseRailPrefs({ order: ["a", "b", "c", "d"], visible: 2 });
    expect(p.hidden).toEqual(["c", "d"]);
  });

  test("an explicit hidden list wins over a leftover count", () => {
    const p = parseRailPrefs({ order: ["a", "b", "c"], visible: 1, hidden: ["b"] });
    expect(p.hidden).toEqual(["b"]);
  });

  test("a count with nothing to fold leaves nothing hidden", () => {
    expect(parseRailPrefs({ order: ["a", "b"], visible: 5 }).hidden).toEqual([]);
  });
});

describe("resolveHidden", () => {
  // A rail that ships every tab expanded runs the height of the window and
  // makes the user tidy up something they never chose.
  test("an unarranged rail starts with the default folded away", () => {
    const hidden = resolveHidden({ order: [], hidden: null }, tabs);
    expect(hidden.length).toBeGreaterThan(0);
    expect(hidden.length).toBeLessThan(tabs.length);
    for (const id of hidden) expect(DEFAULT_HIDDEN).toContain(id);
  });

  // The default names the quiet panels rather than cutting at a position.
  // Cutting folded Source — the panel most likely to be wanted — purely for
  // sitting last in the built-in list.
  test("the default keeps the panels worth seeing", () => {
    const hidden = resolveHidden({ order: [], hidden: null }, tabs);
    expect(hidden).not.toContain("source");
    expect(hidden).not.toContain("notes");
    expect(hidden).not.toContain("ticket");
  });

  test("a chosen list is used as given", () => {
    expect(resolveHidden({ order: [], hidden: ["notes"] }, tabs)).toEqual(["notes"]);
  });

  // The one case the null/[] split exists for.
  test("choosing to unfold everything is respected", () => {
    expect(resolveHidden({ order: [], hidden: [] }, tabs)).toEqual([]);
  });

  // Resolved against the tabs present now rather than stored, so the result
  // never names a panel that is not there — Browser needs an instance, and a
  // phantom entry would linger in the layout the first time it is edited.
  test("absent panels are not named", () => {
    const only = [{ id: "ticket" }, { id: "notes" }];
    expect(resolveHidden({ order: [], hidden: null }, only)).toEqual([]);
  });

  // Naming rather than cutting also means the default does not move when
  // panels are reordered.
  test("reordering does not change the default", () => {
    const forward = resolveHidden({ order: [], hidden: null }, tabs);
    const backward = resolveHidden({ order: [], hidden: null }, [...tabs].reverse());
    expect(new Set(backward)).toEqual(new Set(forward));
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
  test("nothing hidden means everything is in the strip", () => {
    const { shown, overflow } = splitRail(tabs, [], nothingLoud);
    expect(ids(shown)).toEqual(ids(tabs));
    expect(overflow).toEqual([]);
  });

  // Membership, not a count: naming the tabs is what lets someone hide
  // Browser and Workspace and keep everything else, in any order.
  test("the named tabs fold, the rest stay", () => {
    const { shown, overflow } = splitRail(tabs, ["browser", "workspace"], nothingLoud);
    expect(ids(overflow)).toEqual(["workspace", "browser"]);
    expect(ids(shown)).not.toContain("browser");
    expect(ids(shown)).toHaveLength(tabs.length - 2);
  });

  test("hiding one tab does not disturb the others", () => {
    const { shown } = splitRail(tabs, ["context"], nothingLoud);
    expect(ids(shown)).toEqual([
      "ticket", "notes", "process", "workspace", "scheduled", "browser", "source",
    ]);
  });

  // The point of promotion: a count nobody can see is worse than a tab in
  // an unexpected slot.
  test("a loud tab is shown even while folded", () => {
    const { shown, overflow } = splitRail(tabs, ["source"], (id) => id === "source");
    expect(ids(shown)).toContain("source");
    expect(ids(overflow)).not.toContain("source");
  });

  // Promotion is a display override, not an edit — the tab is still folded,
  // so it returns to More the moment it goes quiet.
  test("promotion does not change the saved layout", () => {
    const hidden = ["source"];
    splitRail(tabs, hidden, (id) => id === "source");
    expect(hidden).toEqual(["source"]);
    const { overflow } = splitRail(tabs, hidden, nothingLoud);
    expect(ids(overflow)).toContain("source");
  });

  test("promotion keeps the user's order", () => {
    const { shown } = splitRail(tabs, ["scheduled", "source"], (id) => id === "source");
    // source was folded but is loud; it appears where it always sat, last.
    expect(ids(shown).at(-1)).toBe("source");
  });

  // The open panel's own tab must stay clickable: hiding it would leave a
  // panel with no way to close it.
  test("the active tab is kept in the strip", () => {
    const { shown, overflow } = splitRail(tabs, ["source"], nothingLoud, "source");
    expect(ids(shown)).toContain("source");
    expect(ids(overflow)).not.toContain("source");
  });

  // A tab that shipped after the layout was saved is not named in `hidden`,
  // so it appears — the opposite of a count, which would have folded it.
  test("a tab the layout never mentioned is shown", () => {
    const { shown } = splitRail(tabs, ["browser"], nothingLoud);
    expect(ids(shown)).toContain("source");
  });
});

describe("toggleHidden", () => {
  test("folds a tab that was showing", () => {
    expect(toggleHidden([], "browser")).toEqual(["browser"]);
  });
  test("brings back a tab that was folded", () => {
    expect(toggleHidden(["browser", "notes"], "browser")).toEqual(["notes"]);
  });
  test("does not mutate the input", () => {
    const hidden = ["browser"];
    toggleHidden(hidden, "notes");
    expect(hidden).toEqual(["browser"]);
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

describe("reorderTo", () => {
  const order = ["a", "b", "c", "d", "e"];

  // A drag is not a chain of swaps: the tabs the dragged one passed have to
  // keep their own relative order, which swapping would scramble.
  test("moves to an absolute position, shifting the rest along", () => {
    expect(reorderTo(order, "e", 1)).toEqual(["a", "e", "b", "c", "d"]);
    expect(reorderTo(order, "a", 3)).toEqual(["b", "c", "d", "a", "e"]);
  });

  test("a drop past either end lands at that end", () => {
    expect(reorderTo(order, "c", -5)).toEqual(["c", "a", "b", "d", "e"]);
    expect(reorderTo(order, "c", 99)).toEqual(["a", "b", "d", "e", "c"]);
  });

  test("dropping a tab where it already is changes nothing", () => {
    expect(reorderTo(order, "c", 2)).toBe(order);
  });

  test("an unknown id is left alone", () => {
    expect(reorderTo(order, "zzz", 0)).toBe(order);
  });

  // Order only. Folding is its own list now, so dragging a tab past another
  // must not hide it as a side effect.
  test("reordering never changes what is hidden", () => {
    const hidden = ["c"];
    const moved = reorderTo(order, "a", 4);
    expect(splitRail(moved.map((id) => ({ id })), hidden, () => false).overflow)
      .toEqual([{ id: "c" }]);
  });
});

describe("railPrefsFromPage", () => {
  const el = (attr?: string) => {
    const d = document.createElement("div");
    if (attr !== undefined) d.dataset.railPrefs = attr;
    return d;
  };

  // The strip paints on the first frame, so the saved layout has to be there
  // already — fetching it meant drawing the default and then collapsing.
  test("reads the layout the shell inlined", () => {
    const got = railPrefsFromPage(el(JSON.stringify({ order: ["notes"], hidden: ["browser"] })));
    expect(got).toEqual({ order: ["notes"], hidden: ["browser"] });
  });

  test("normalises what it finds", () => {
    const got = railPrefsFromPage(el(JSON.stringify({ order: ["a", 7], hidden: [null, "b"] })));
    expect(got).toEqual({ order: ["a"], hidden: ["b"] });
  });

  // null, not a default: absent means "this shell did not carry it", and the
  // caller has to fetch rather than paint a guess as though it were saved.
  test("null when the shell carried nothing", () => {
    expect(railPrefsFromPage(el())).toBeNull();
    expect(railPrefsFromPage(el(""))).toBeNull();
    expect(railPrefsFromPage(null)).toBeNull();
  });

  test("null on unparseable json", () => {
    expect(railPrefsFromPage(el("{not json"))).toBeNull();
  });
});
