import { describe, expect, it } from "vitest";
import { filterQuery, readFilter, type PageFilter } from "$lib/urlFilter";

const base: PageFilter = { range: 1, from: "", to: "", channels: [], instances: [], providers: [] };

describe("url filter", () => {
  it("leaves the default view out of the URL", () => {
    expect(filterQuery(base)).toBe("");
    expect(readFilter("")).toEqual(base);
  });

  it("round-trips channels and providers, (none) included", () => {
    const f: PageFilter = {
      ...base,
      range: 30,
      channels: ["ui", "slack"],
      providers: ["claude/acct-a", "(none)"],
    };
    const q = filterQuery(f);
    expect(q).toContain("providers=");
    expect(readFilter(q)).toEqual(f);
  });

  it("keeps a custom range only with both dates", () => {
    const f: PageFilter = { ...base, range: "custom", from: "2026-01-01", to: "2026-01-31" };
    expect(readFilter(filterQuery(f))).toEqual(f);
    expect(readFilter("?range=custom&from=2026-01-01").range).toBe(1);
  });

  it("falls back to the default on a range it does not know", () => {
    expect(readFilter("?range=banana").range).toBe(1);
    expect(readFilter("?range=all").range).toBe("all");
    expect(readFilter("?providers=all,,codex/acct-b").providers).toEqual(["codex/acct-b"]);
  });
});
