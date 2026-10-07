import { describe, it, expect } from "vitest";
import { withModelListMeta, modelListMeta, describeModelListMeta } from "../model-list-meta.js";

describe("model-list-meta", () => {
  it("rides on the array without changing it", () => {
    const list = withModelListMeta([{ id: "a" }], { fetched_at: "2026-10-01T00:55:00Z", source: "files", can_refresh: true });
    expect(list).toEqual([{ id: "a" }]);
    expect(Object.keys(list)).toEqual(["0"]);
    expect(modelListMeta(list)).toEqual({ fetchedAt: "2026-10-01T00:55:00Z", source: "files", canRefresh: true });
  });
  it("is absent for a server that sent no stamp", () => {
    expect(modelListMeta(withModelListMeta([], {}))).toBeUndefined();
  });
  it("describes the stamp", () => {
    expect(describeModelListMeta({ fetchedAt: "2026-10-01T00:55:00Z", source: "cli" })).toMatch(/^Updated .+ · cli$/);
    expect(describeModelListMeta({ canRefresh: true })).toBe("No model list yet — click Refresh");
  });
});
