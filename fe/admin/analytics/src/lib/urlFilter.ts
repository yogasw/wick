/** The page's filter bar, kept in the address bar so a link reproduces
 *  the view: same range, same channels, same bots, same providers. */

/** "all" runs back to the oldest conversation; "custom" uses the dates. */
export type Range = 1 | 7 | 30 | 90 | 365 | "all" | "custom";

/** One row of the condition builder: a field, an operator, a value. */
export type CondField = "title" | "session" | "any" | "model" | "provider" | "person" | "channel" | "project" | "agent";
export type CondOp = "contains" | "not_contains" | "exact" | "not_exact" | "regex" | "not_regex";
export type Condition = { field: CondField; op: CondOp; value: string };
/** Rows joined by AND (every one holds) or OR (any one is enough). */
export type Conditions = { join: "and" | "or"; rules: Condition[] };
export const emptyConditions: Conditions = { join: "and", rules: [] };

const condFields = new Set<string>(["title", "session", "any", "model", "provider", "person", "channel", "project", "agent"]);
const condOps = new Set<string>(["contains", "not_contains", "exact", "not_exact", "regex", "not_regex"]);

export type PageFilter = {
  range: Range;
  from: string;
  to: string;
  channels: string[];
  instances: string[];
  providers: string[];
  people: string[];
  models: string[];
  match: Conditions;
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
    match: readConditions(q),
  };
}

/** Each "match" is "<field>:<op>:<value>". A bare value is the older
 *  single-condition link, read with match_in / match_mode. */
function readConditions(q: URLSearchParams): Conditions {
  const legacyIn = q.get("match_in") ?? "title";
  const legacyOp = q.get("match_mode") ?? "contains";
  const rules: Condition[] = [];
  for (const raw of q.getAll("match")) {
    let field = legacyIn;
    let op = legacyOp;
    let value = raw;
    const [a, b, ...rest] = raw.split(":");
    if (rest.length && condFields.has(a) && condOps.has(b)) {
      field = a;
      op = b;
      value = rest.join(":");
    }
    value = value.trim();
    if (!value) continue;
    rules.push({
      field: (condFields.has(field) ? field : "title") as CondField,
      op: (condOps.has(op) ? op : "contains") as CondOp,
      value,
    });
  }
  return { join: q.get("match_op") === "or" ? "or" : "and", rules };
}

/** The rows that say something, in their wire form. */
export function conditionValues(c: Conditions): string[] {
  return c.rules.filter((r) => r.value.trim()).map((r) => `${r.field}:${r.op}:${r.value.trim()}`);
}

/** The conditions as query pairs, [] when there are none. Shared by the
 *  address bar, the page's fetch and the ledger's, so all three agree. */
export function conditionParams(c: Conditions): Array<[string, string]> {
  const values = conditionValues(c);
  const out: Array<[string, string]> = values.map((v) => ["match", v]);
  if (values.length > 1 && c.join === "or") out.push(["match_op", "or"]);
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
  for (const [k, v] of conditionParams(f.match)) q.append(k, v);
  const s = q.toString();
  return s ? `?${s}` : "";
}
