/** The page's filter bar, kept in the address bar so a link reproduces
 *  the view: same range, same channels, same bots, same providers. */

/** "all" runs back to the oldest conversation; "custom" uses the dates. */
export type Range = 1 | 7 | 30 | 90 | 365 | "all" | "custom";

/** Where the text filter looks, and how it reads the pattern. */
export type MatchIn = "title" | "session" | "any";
export type MatchMode = "contains" | "exact" | "regex";
export type TextFilter = { text: string; in: MatchIn; mode: MatchMode };
export const emptyText: TextFilter = { text: "", in: "title", mode: "contains" };

export type PageFilter = {
  range: Range;
  from: string;
  to: string;
  channels: string[];
  instances: string[];
  providers: string[];
  people: string[];
  models: string[];
  match: TextFilter;
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
    people: list(q, "people"),
    models: list(q, "models"),
    match: readText(q),
  };
}

function readText(q: URLSearchParams): TextFilter {
  const text = (q.get("match") ?? "").trim();
  if (!text) return { ...emptyText };
  const rawIn = q.get("match_in");
  const rawMode = q.get("match_mode");
  return {
    text,
    in: rawIn === "session" || rawIn === "any" ? rawIn : "title",
    mode: rawMode === "exact" || rawMode === "regex" ? rawMode : "contains",
  };
}

/** The text filter as query pairs, [] when it is off. Shared by the
 *  address bar, the page's fetch and the ledger's, so all three agree. */
export function textParams(t: TextFilter): Array<[string, string]> {
  const text = t.text.trim();
  if (!text) return [];
  const out: Array<[string, string]> = [["match", text]];
  if (t.in !== "title") out.push(["match_in", t.in]);
  if (t.mode !== "contains") out.push(["match_mode", t.mode]);
  return out;
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
  if (f.people.length) q.set("people", f.people.join(","));
  if (f.models.length) q.set("models", f.models.join(","));
  for (const [k, v] of textParams(f.match)) q.set(k, v);
  const s = q.toString();
  return s ? `?${s}` : "";
}
