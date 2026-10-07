import { describe, it, expect } from "vitest";
import { buildGraph, graphWidth } from "$lib/graph";

describe("buildGraph", () => {
  it("keeps a linear history in one lane", () => {
    const rows = buildGraph([
      { sha: "c", parents: ["b"] },
      { sha: "b", parents: ["a"] },
      { sha: "a", parents: [] },
    ]);
    expect(rows.map((r) => r.lane)).toEqual([0, 0, 0]);
    expect(graphWidth(rows)).toBe(1);
    // The root closes its lane instead of trailing a line off the bottom.
    expect(rows[2].outgoing).toEqual([]);
    // The tip has nothing above it.
    expect(rows[0].incoming).toEqual([]);
  });

  it("opens a lane for a merge's second parent and closes it on the join", () => {
    //  m ── merge of c (mainline) and f (feature)
    const rows = buildGraph([
      { sha: "m", parents: ["c", "f"] },
      { sha: "c", parents: ["b"] },
      { sha: "f", parents: ["b"] },
      { sha: "b", parents: [] },
    ]);
    const [m, c, f, b] = rows;
    expect(m.lane).toBe(0);
    // The feature parent gets a lane of its own, drawn as a diagonal.
    expect(m.merges).toHaveLength(1);
    expect(m.merges[0].lane).toBe(1);
    expect(c.lane).toBe(0);
    expect(f.lane).toBe(1);
    // Both lanes wait for b, so one collapses into the other at b.
    expect(b.lane).toBe(0);
    expect(b.collapses.map((x) => x.lane)).toEqual([1]);
    expect(b.outgoing).toEqual([]);
    expect(graphWidth(rows)).toBe(2);
  });

  it("gives disjoint tips their own lanes", () => {
    // Two branches with no shared history, as `all` would return.
    const rows = buildGraph([
      { sha: "x", parents: [] },
      { sha: "y", parents: [] },
    ]);
    expect(rows[0].lane).toBe(0);
    // x's lane closed at its root, so y is free to reuse it.
    expect(rows[1].lane).toBe(0);
  });

  it("survives a parent that is not in the window", () => {
    // The last row of a --max-count'd log points at a commit we never see.
    const rows = buildGraph([{ sha: "a", parents: ["older"] }]);
    expect(rows[0].outgoing).toEqual([{ lane: 0, color: 0 }]);
  });
});
