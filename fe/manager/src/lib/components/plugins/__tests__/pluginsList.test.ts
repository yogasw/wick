import { describe, it, expect } from "vitest";
import type { InstalledPlugin } from "$lib/api.js";
import {
  DEFAULT_QUERY, parseQuery, queryString, filterInstalled, facetCounts, sortInstalled, paginate,
  marketRows, filterMarket, versionNewer,
} from "../pluginsList.js";

function plugin(over: Partial<InstalledPlugin>): InstalledPlugin {
  return { key: "x", name: "X", kind: "connector", version: "1.0.0", enabled: true, detail_path: "/connectors/x",
    origin: "local", update_available: false, last_health_ok: false, ...over };
}

const list = [
  plugin({ key: "loki", name: "Loki", origin: "official", update_available: true, last_check_at: "2026-10-01T00:00:00Z" }),
  plugin({ key: "echo", name: "Echo", kind: "tool", origin: "url-zip", enabled: false, last_check_at: "2026-10-03T00:00:00Z" }),
  plugin({ key: "up", name: "Uploaded", kind: "job", origin: "upload", last_health_at: "2026-10-01T00:00:00Z" }),
];

describe("pluginsList query string", () => {
  it("round-trips non-defaults and drops defaults", () => {
    expect(queryString(DEFAULT_QUERY)).toBe("");
    const q = { ...DEFAULT_QUERY, q: " lo ", kind: "tool" as const, status: "update" as const, page: 2, size: 50 };
    const s = queryString(q);
    expect(s).toBe("?q=lo&kind=tool&status=update&page=2&size=50");
    expect(parseQuery(s)).toEqual({ ...q, q: "lo" });
  });

  it("ignores junk values", () => {
    expect(parseQuery("?tab=nope&kind=x&page=-3&size=7&sort=zzz")).toEqual(DEFAULT_QUERY);
  });
});

describe("pluginsList filter/sort/paginate", () => {
  const f = { q: "", kind: "all" as const, origin: "all" as const, status: "all" as const };

  it("searches name/key and groups url-zip under source", () => {
    expect(filterInstalled(list, { ...f, q: "LOK" }).map((p) => p.key)).toEqual(["loki"]);
    expect(filterInstalled(list, { ...f, origin: "source" }).map((p) => p.key)).toEqual(["echo"]);
    expect(filterInstalled(list, { ...f, status: "error" }).map((p) => p.key)).toEqual(["up"]);
    expect(filterInstalled(list, { ...f, status: "disabled" }).map((p) => p.key)).toEqual(["echo"]);
  });

  it("counts each facet option with the other facets applied", () => {
    const c = facetCounts(list, { ...f, kind: "tool" });
    expect(c.kind).toMatchObject({ all: 3, connector: 1, tool: 1, job: 1 });
    expect(c.status).toMatchObject({ all: 1, disabled: 1, active: 0 });
  });

  it("sorts by name, kind, last check and updates first", () => {
    expect(sortInstalled(list, "name").map((p) => p.key)).toEqual(["echo", "loki", "up"]);
    expect(sortInstalled(list, "kind").map((p) => p.key)).toEqual(["loki", "up", "echo"]);
    expect(sortInstalled(list, "checked").map((p) => p.key)).toEqual(["echo", "loki", "up"]);
    expect(sortInstalled(list, "update")[0].key).toBe("loki");
  });

  it("paginates and clamps a stale page", () => {
    const n = Array.from({ length: 34 }, (_, i) => i);
    expect(paginate(n, 1, 25)).toMatchObject({ page: 1, pages: 2, from: 1, to: 25, total: 34 });
    expect(paginate(n, 9, 25)).toMatchObject({ page: 2, from: 26, to: 34 });
    expect(paginate([], 1, 25)).toMatchObject({ page: 1, pages: 1, from: 0, to: 0, total: 0 });
  });
});

describe("marketplace rules", () => {
  it("maps the old Available tab to Marketplace", () => {
    expect(parseQuery("?tab=available").tab).toBe("marketplace");
  });

  it("compares versions numerically", () => {
    expect(versionNewer("1.10.0", "1.9.2")).toBe(true);
    expect(versionNewer("v1.2.0", "1.2.0")).toBe(false);
    expect(versionNewer("1.2.0-rc1", "1.1.9")).toBe(true);
  });

  it("derives one row per (source, key) with its install state", () => {
    const inst = [
      plugin({ key: "a", kind: "tool", version: "1.0.0", origin: "source", source_id: "s1" }),
      plugin({ key: "b", kind: "tool", version: "2.0.0", origin: "source", source_id: "s1" }),
      plugin({ key: "c", kind: "connector", version: "1.0.0", origin: "official" }),
    ];
    const av = (key: string, version: string, source_id = "s1", arch_ok = true) =>
      ({ source_id, source_name: source_id, key, kind: "tool", name: key, version, arch_ok, os_arch: [] });
    const rows = marketRows(
      [av("a", "1.1.0"), av("b", "1.0.0"), av("a", "1.0.0", "s2"), av("d", "1.0.0", "s1", false), av("e", "1.0.0")],
      [{ key: "c", name: "C", description: "", version: "1.0.0", installed: false, enabled: false, arch_ok: true, signed: "none" }],
      inst,
    );
    expect(Object.fromEntries(rows.map((r) => [r.id, r.state]))).toEqual({
      "official:c": "installed", "s1:a": "update", "s1:b": "installed", "s2:a": "other", "s1:d": "no-arch", "s1:e": "install",
    });
    expect(filterMarket(rows, { q: "", avail: "installed", src: "all" })).toHaveLength(4);
    expect(filterMarket(rows, { q: "", avail: "not-installed", src: "s1" }).map((r) => r.key)).toEqual(["d", "e"]);
  });
});
