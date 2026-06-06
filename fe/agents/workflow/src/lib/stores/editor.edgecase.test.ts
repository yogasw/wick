import { describe, it, expect } from "vitest";
import { applyEdgeCase } from "./editor";
import type { Edge } from "$lib/types/workflow";

describe("applyEdgeCase", () => {
  it("sets a case on a case-less edge", () => {
    const edges: Edge[] = [{ from: "route", to: "s1" }];
    const out = applyEdgeCase(edges, "route", "s1", undefined, "yes");
    expect(out).toEqual([{ from: "route", to: "s1", case: "yes" }]);
  });

  it("changes an existing case", () => {
    const edges: Edge[] = [{ from: "route", to: "start", case: "default" }];
    const out = applyEdgeCase(edges, "route", "start", "default", "no");
    expect(out[0].case).toBe("no");
  });

  it("clears the case when nextCase is empty", () => {
    const edges: Edge[] = [{ from: "route", to: "s1", case: "yes" }];
    const out = applyEdgeCase(edges, "route", "s1", "yes", "");
    expect(out[0].case).toBeUndefined();
  });

  it("only touches the matching edge", () => {
    const edges: Edge[] = [
      { from: "route", to: "s1", case: "yes" },
      { from: "route", to: "start", case: "default" },
      { from: "a", to: "b" },
    ];
    const out = applyEdgeCase(edges, "route", "s1", "yes", "match");
    expect(out[0].case).toBe("match");
    expect(out[1].case).toBe("default");
    expect(out[2].case).toBeUndefined();
  });

  it("matches on prevCase so duplicate from/to edges are disambiguated", () => {
    const edges: Edge[] = [
      { from: "route", to: "t", case: "yes" },
      { from: "route", to: "t", case: "no" },
    ];
    const out = applyEdgeCase(edges, "route", "t", "no", "default");
    expect(out[0].case).toBe("yes");
    expect(out[1].case).toBe("default");
  });

  it("does not mutate the input array", () => {
    const edges: Edge[] = [{ from: "route", to: "s1" }];
    applyEdgeCase(edges, "route", "s1", undefined, "yes");
    expect(edges[0].case).toBeUndefined();
  });
});
