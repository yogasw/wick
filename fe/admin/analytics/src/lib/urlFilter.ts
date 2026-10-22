/** The page's filter bar, kept in the address bar so a link reproduces
 *  the view: same range, same channels, same bots, same providers. */

/** "all" runs back to the oldest conversation; "custom" uses the dates. */
export type Range = 1 | 7 | 30 | 90 | 365 | "all" | "custom";

export type PageFilter = {
  range: Range;
  from: string;
  to: string;
  channels: string[];
  instances: string[];
  providers: string[];
};

/** Today: the page's own default, so it never needs to be in the URL. */
export const defaultRange: Range = 1;

const dayRanges = new Set([1, 7, 30, 90, 365]);

function list(q: URLSearchParams, key: string): string[] {
  return (q.get(key) ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s && s.toLowerCase() !== "all");
}

/** Reads the filter off a query string. Anything unrecognised falls back
 *  to the default rather than to an error: a stale link should still open. */
export function readFilter(search: string): PageFilter {
  const q = new URLSearchParams(search);
  const raw = q.get("range") ?? "";
  let range: Range = defaultRange;
  if (raw === "all" || raw === "custom") range = raw;
  else if (dayRanges.has(Number(raw))) range = Number(raw) as Range;
  const from = q.get("from") ?? "";
  const to = q.get("to") ?? "";
  if (range === "custom" && (!from || !to)) range = defaultRange;
  return {
    range,
    from: range === "custom" ? from : "",
    to: range === "custom" ? to : "",
    channels: list(q, "channels"),
    instances: list(q, "instances"),
    providers: list(q, "providers"),
  };
}

/** The query string for a filter, "" when everything is the default. */
export function filterQuery(f: PageFilter): string {
  const q = new URLSearchParams();
  if (f.range !== defaultRange) q.set("range", String(f.range));
  if (f.range === "custom") {
    if (f.from) q.set("from", f.from);
    if (f.to) q.set("to", f.to);
  }
  if (f.channels.length) q.set("channels", f.channels.join(","));
  if (f.instances.length) q.set("instances", f.instances.join(","));
  if (f.providers.length) q.set("providers", f.providers.join(","));
  const s = q.toString();
  return s ? `?${s}` : "";
}
