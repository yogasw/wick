import { describe, expect, it } from "vitest";
import { filterQuery, readFilter, type PageFilter } from "$lib/urlFilter";

const base: PageFilter = { range: 1, from: "", to: "", channels: [], instances: [], providers: [], people: [], models: [], match: { text: "", in: "title", mode: "contains" } };

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
      people: ["user-a", "(unattributed)"],
      models: ["opus"],
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

  it("round-trips the title / session id filter, defaults left out", () => {
    const f: PageFilter = { ...base, match: { text: "^daily-\\d+", in: "session", mode: "regex" } };
    const q = filterQuery(f);
    expect(q).toContain("match_in=session");
    expect(readFilter(q)).toEqual(f);
    const plain = filterQuery({ ...base, match: { text: "report", in: "title", mode: "contains" } });
    expect(plain).toBe("?match=report");
  });

  it("reads an unknown match field or mode as the defaults", () => {
    expect(readFilter("?match=x&match_in=bogus&match_mode=bogus").match).toEqual({ text: "x", in: "title", mode: "contains" });
  });
});
