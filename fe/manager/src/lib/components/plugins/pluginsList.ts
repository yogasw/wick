/* List state for Admin → Plugins: search, facets, sort and pagination, kept
   in the query string so a reload or a shared link lands on the same view.
   Pure functions only — the component owns the state, this owns the rules. */
import type { AvailablePlugin, InstalledPlugin, PluginOrigin } from "$lib/api.js";
import type { PluginEntry } from "$lib/types.js";

export type Tab = "installed" | "marketplace" | "sources" | "add";
export type KindFilter = "all" | InstalledPlugin["kind"];
export type OriginFilter = "all" | "official" | "source" | "upload" | "local";
export type StatusFilter = "all" | "active" | "disabled" | "update" | "error";
export type SortKey = "name" | "kind" | "checked" | "update";
export type MarketFilter = "all" | "not-installed" | "installed" | "update";
export type MarketSort = "name" | "updated";

export const TABS: Tab[] = ["installed", "marketplace", "sources", "add"];
export const MARKET_FILTERS: MarketFilter[] = ["all", "not-installed", "installed", "update"];
export const MARKET_SORTS: MarketSort[] = ["name", "updated"];
export const KINDS: KindFilter[] = ["all", "connector", "tool", "job", "service"];
export const ORIGINS: OriginFilter[] = ["all", "official", "source", "upload", "local"];
export const STATUSES: StatusFilter[] = ["all", "active", "disabled", "update", "error"];
export const SORTS: SortKey[] = ["name", "kind", "checked", "update"];
export const PAGE_SIZES = [25, 50, 100];

export interface ListQuery {
  tab: Tab;
  q: string;
  kind: KindFilter;
  origin: OriginFilter;
  status: StatusFilter;
  sort: SortKey;
  page: number;
  size: number;
  /* Marketplace only: install state, source ("all" | "official" | source
     id) and sort. */
  avail: MarketFilter;
  src: string;
  msort: MarketSort;
}

export const DEFAULT_QUERY: ListQuery = {
  tab: "installed", q: "", kind: "all", origin: "all", status: "all", sort: "name", page: 1, size: 25,
  avail: "all", src: "all", msort: "name",
};

function pick<T extends string>(v: string | null, allowed: readonly T[], fallback: T): T {
  return v !== null && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
}

export function parseQuery(search: string): ListQuery {
  const p = new URLSearchParams(search);
  const page = Number.parseInt(p.get("page") ?? "", 10);
  const size = Number.parseInt(p.get("size") ?? "", 10);
  // The Marketplace tab was called Available; old links still land on it.
  const tab = p.get("tab") === "available" ? "marketplace" : p.get("tab");
  return {
    tab: pick(tab, TABS, DEFAULT_QUERY.tab),
    q: p.get("q") ?? "",
    kind: pick(p.get("kind"), KINDS, "all"),
    origin: pick(p.get("origin"), ORIGINS, "all"),
    status: pick(p.get("status"), STATUSES, "all"),
    sort: pick(p.get("sort"), SORTS, DEFAULT_QUERY.sort),
    page: page > 0 ? page : 1,
    size: PAGE_SIZES.includes(size) ? size : DEFAULT_QUERY.size,
    avail: pick(p.get("avail"), MARKET_FILTERS, "all"),
    src: p.get("src") || "all",
    msort: pick(p.get("msort"), MARKET_SORTS, "name"),
  };
}

/* Only non-default values, so the plain page keeps a clean URL. Returns ""
   or "?…". */
export function queryString(q: ListQuery): string {
  const p = new URLSearchParams();
  (Object.keys(DEFAULT_QUERY) as (keyof ListQuery)[]).forEach((k) => {
    const v = k === "q" ? q.q.trim() : q[k];
    if (v !== DEFAULT_QUERY[k]) p.set(k, String(v));
  });
  const s = p.toString();
  return s ? `?${s}` : "";
}

export const originGroup = (o: PluginOrigin): OriginFilter => (o === "url-zip" ? "source" : o);
export const hasError = (p: InstalledPlugin) => !!p.last_health_at && !p.last_health_ok;
export const canUpdate = (p: InstalledPlugin) => p.update_available && p.origin !== "upload" && p.origin !== "local";
export const hasUpdateSource = (p: InstalledPlugin) => p.origin !== "upload" && p.origin !== "local";

export function matchesStatus(p: InstalledPlugin, s: StatusFilter): boolean {
  switch (s) {
    case "active": return p.enabled;
    case "disabled": return !p.enabled;
    case "update": return p.update_available;
    case "error": return hasError(p);
    default: return true;
  }
}

/* Case-insensitive substring over any of the given fields. */
export function matchesText(q: string, ...fields: (string | undefined)[]): boolean {
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  return fields.some((f) => (f ?? "").toLowerCase().includes(needle));
}

const textOf = (p: InstalledPlugin) => [p.name, p.key, p.kind, p.source_name];

type Facets = Pick<ListQuery, "q" | "kind" | "origin" | "status">;

function matches(p: InstalledPlugin, f: Facets, skip?: "kind" | "origin" | "status"): boolean {
  return matchesText(f.q, ...textOf(p))
    && (skip === "kind" || f.kind === "all" || p.kind === f.kind)
    && (skip === "origin" || f.origin === "all" || originGroup(p.origin) === f.origin)
    && (skip === "status" || matchesStatus(p, f.status));
}

export function filterInstalled(list: InstalledPlugin[], f: Facets): InstalledPlugin[] {
  return list.filter((p) => matches(p, f));
}

/* Per-option counts for each facet, with the OTHER facets (and the search)
   applied — what picking that option would leave on screen. */
export function facetCounts(list: InstalledPlugin[], f: Facets) {
  const kind: Record<string, number> = {};
  const origin: Record<string, number> = {};
  const status: Record<string, number> = {};
  for (const k of KINDS) kind[k] = list.filter((p) => matches(p, { ...f, kind: k }, undefined)).length;
  for (const o of ORIGINS) origin[o] = list.filter((p) => matches(p, { ...f, origin: o }, undefined)).length;
  for (const s of STATUSES) status[s] = list.filter((p) => matches(p, { ...f, status: s }, undefined)).length;
  return { kind, origin, status };
}

const byName = (a: InstalledPlugin, b: InstalledPlugin) =>
  (a.name || a.key).localeCompare(b.name || b.key) || a.kind.localeCompare(b.kind);

export function sortInstalled(list: InstalledPlugin[], sort: SortKey): InstalledPlugin[] {
  const out = [...list];
  switch (sort) {
    case "kind":
      return out.sort((a, b) => a.kind.localeCompare(b.kind) || byName(a, b));
    case "checked":
      // Most recently checked first; never-checked plugins sink.
      return out.sort((a, b) => (b.last_check_at ?? "").localeCompare(a.last_check_at ?? "") || byName(a, b));
    case "update":
      return out.sort((a, b) => Number(b.update_available) - Number(a.update_available) || byName(a, b));
    default:
      return out.sort(byName);
  }
}

export interface Page<T> {
  rows: T[];
  page: number;
  pages: number;
  from: number;
  to: number;
  total: number;
}

/* page is clamped into range, so a stale ?page=9 after a filter still shows
   rows instead of an empty table. */
export function paginate<T>(list: T[], page: number, size: number): Page<T> {
  const total = list.length;
  const pages = Math.max(1, Math.ceil(total / size));
  const cur = Math.min(Math.max(1, page), pages);
  const start = (cur - 1) * size;
  const rows = list.slice(start, start + size);
  return { rows, page: cur, pages, from: total ? start + 1 : 0, to: start + rows.length, total };
}

/* Marketplace: one card per (source, key) across the official catalog and
   every source. Installed plugins stay listed with their state:
   install     — not installed, a build exists for this host
   no-arch     — not installed, no build for this host
   installed   — installed from this source, same or newer version
   update      — installed from this source, the source offers newer
   other       — installed from another source/origin; no Install, so one
                 key never ends up installed twice. */
export type MarketState = "install" | "no-arch" | "installed" | "update" | "other";
export interface MarketRow {
  id: string;
  sourceID: string; // "official" or the source id
  sourceName: string;
  key: string;
  kind: InstalledPlugin["kind"];
  name: string;
  description: string;
  version: string;
  state: MarketState;
  installed?: InstalledPlugin;
  checkedAt?: string;
  src?: AvailablePlugin;
  off?: PluginEntry;
}

const KIND_SET = new Set(["connector", "tool", "job", "service"]);
const asKind = (k: string): InstalledPlugin["kind"] => (KIND_SET.has(k) ? (k as InstalledPlugin["kind"]) : "connector");

/* Numeric dot compare ("v1.10.0" > "1.9.2"); a pre-release tail is ignored. */
export function versionNewer(a: string, b: string): boolean {
  const parts = (v: string) => v.replace(/^v/, "").split(/[-+]/)[0].split(".").map((n) => Number.parseInt(n, 10) || 0);
  const x = parts(a);
  const y = parts(b);
  for (let i = 0; i < Math.max(x.length, y.length); i++) {
    if ((x[i] ?? 0) !== (y[i] ?? 0)) return (x[i] ?? 0) > (y[i] ?? 0);
  }
  return false;
}

function stateOf(inst: InstalledPlugin | undefined, sameOrigin: boolean, version: string, archOK: boolean): MarketState {
  if (!inst) return archOK ? "install" : "no-arch";
  if (!sameOrigin) return "other";
  return versionNewer(version, inst.version) ? "update" : "installed";
}

export function marketRows(
  available: AvailablePlugin[], official: PluginEntry[], installed: InstalledPlugin[],
  checked: Record<string, string | undefined> = {},
): MarketRow[] {
  const find = (key: string, kind: string) => installed.find((p) => p.key === key && p.kind === kind);
  const rows: MarketRow[] = official.map((e) => {
    const inst = find(e.key, "connector");
    return {
      id: `official:${e.key}`, sourceID: "official", sourceName: "Official wick", key: e.key, kind: "connector",
      name: e.name, description: e.description, version: e.version, installed: inst, checkedAt: checked.official, off: e,
      state: stateOf(inst, inst?.origin === "official", e.version, e.arch_ok),
    };
  });
  for (const a of available) {
    const kind = asKind(a.kind);
    const inst = find(a.key, kind);
    rows.push({
      id: `${a.source_id}:${a.key}`, sourceID: a.source_id, sourceName: a.source_name, key: a.key, kind,
      name: a.name, description: a.description ?? "", version: a.version, installed: inst, checkedAt: checked[a.source_id], src: a,
      state: stateOf(inst, !!inst && inst.source_id === a.source_id, a.version, a.arch_ok),
    });
  }
  return rows;
}

export function matchesMarket(r: MarketRow, f: MarketFilter): boolean {
  switch (f) {
    case "not-installed": return r.state === "install" || r.state === "no-arch";
    case "installed": return !!r.installed;
    case "update": return r.state === "update";
    default: return true;
  }
}

export function filterMarket(rows: MarketRow[], f: Pick<ListQuery, "q" | "avail" | "src">): MarketRow[] {
  return rows.filter((r) => matchesText(f.q, r.name, r.key, r.description, r.sourceName, r.kind)
    && (f.src === "all" || r.sourceID === f.src)
    && matchesMarket(r, f.avail));
}

/* Recently updated: rows with an update first, then the most recently
   checked source (entries carry no publish date), then name. */
export function sortMarket(rows: MarketRow[], sort: MarketSort): MarketRow[] {
  const byName = (a: MarketRow, b: MarketRow) =>
    (a.name || a.key).localeCompare(b.name || b.key) || a.sourceName.localeCompare(b.sourceName);
  const out = [...rows];
  if (sort === "updated") {
    return out.sort((a, b) => Number(b.state === "update") - Number(a.state === "update")
      || (b.checkedAt ?? "").localeCompare(a.checkedAt ?? "") || byName(a, b));
  }
  return out.sort(byName);
}

/* Confirm-dialog body for Uninstall, per kind. */
export function uninstallBody(kind: InstalledPlugin["kind"]): string {
  const keep = "Its config stays, so reinstalling picks it up again.";
  switch (kind) {
    case "service": return `Stops the service first, then deletes the plugin from this wick. ${keep}`;
    case "job": return `Unschedules the job and deletes the plugin from this wick. ${keep}`;
    case "tool": return `Stops the tool and deletes the plugin from this wick. ${keep}`;
    default: return `Deletes the connector plugin from this wick. ${keep}`;
  }
}
