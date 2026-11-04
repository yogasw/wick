/* The audit page's advanced filter: a one-line query such as

     connector:slack op:send,get_thread status:error user:"Dev One" latency>1000 from:2026-11-04T09:10 timeout

   parsed into AuditFilter fields and written back from them, so the query
   box and the dropdowns always show the same filter. A bare word (or a
   quoted phrase) without a key searches the error text. */
import type { AuditFilter } from "$lib/types.js";

export type QueryKey =
  | "connector" | "instance" | "op" | "source" | "status" | "user" | "session" | "error" | "from" | "to";

/* Accepted spellings per key; the first is the one written back. */
const KEY_ALIASES: Record<QueryKey, string[]> = {
  connector: ["connector", "type"],
  instance: ["instance", "inst"],
  op: ["op", "operation"],
  source: ["source", "src"],
  status: ["status"],
  user: ["user", "by"],
  session: ["session", "sess"],
  error: ["error", "err"],
  from: ["from", "since"],
  to: ["to", "until"],
};

const aliasToKey = new Map<string, QueryKey>();
for (const [key, names] of Object.entries(KEY_ALIASES) as [QueryKey, string[]][]) {
  for (const n of names) aliasToKey.set(n, key);
}

export const QUERY_KEYS = Object.keys(KEY_ALIASES) as QueryKey[];

export interface ParsedQuery {
  /* Raw values per key, as typed (names are resolved by the caller). */
  values: Partial<Record<QueryKey, string>>;
  minMs: string;
  maxMs: string;
  /* Tokens that could not be read, to show back to the person. */
  unknown: string[];
}

/* tokenize splits on spaces outside double quotes and drops the quotes. */
function tokenize(text: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quoted = false;
  let had = false;
  for (const ch of text) {
    if (ch === '"') {
      quoted = !quoted;
      had = true;
    } else if (!quoted && /\s/.test(ch)) {
      if (cur || had) out.push(cur);
      cur = "";
      had = false;
    } else {
      cur += ch;
    }
  }
  if (cur || had) out.push(cur);
  return out;
}

export function parseAuditQuery(text: string): ParsedQuery {
  const res: ParsedQuery = { values: {}, minMs: "", maxMs: "", unknown: [] };
  const free: string[] = [];
  for (const tok of tokenize(text.trim())) {
    const lat = /^(?:latency|ms)\s*([<>])=?\s*(\d+)$/i.exec(tok);
    if (lat) {
      if (lat[1] === ">") res.minMs = lat[2];
      else res.maxMs = lat[2];
      continue;
    }
    const m = /^([a-z_]+):(.*)$/i.exec(tok);
    if (m) {
      const key = aliasToKey.get(m[1].toLowerCase());
      if (!key) {
        res.unknown.push(tok);
        continue;
      }
      if (m[2] !== "") res.values[key] = m[2];
      continue;
    }
    free.push(tok);
  }
  if (free.length > 0 && !res.values.error) res.values.error = free.join(" ");
  return res;
}

function quote(v: string): string {
  return /[\s"]/.test(v) ? `"${v.replace(/"/g, "")}"` : v;
}

/* formatAuditQuery writes a filter back as a query. display maps a raw
   value (an id) to what the person would type (a name). */
export function formatAuditQuery(
  f: AuditFilter,
  display: (key: QueryKey, value: string) => string = (_k, v) => v,
): string {
  const parts: string[] = [];
  for (const key of QUERY_KEYS) {
    const v = f[key];
    if (v) parts.push(`${KEY_ALIASES[key][0]}:${quote(display(key, v))}`);
  }
  if (f.min_ms) parts.push(`latency>${f.min_ms}`);
  if (f.max_ms) parts.push(`latency<${f.max_ms}`);
  return parts.join(" ");
}

/* toISOFromLocal turns a datetime-local value (or a bare date) into what
   the API takes; a date stays a date so "to" keeps covering its day. */
export function toISOFromLocal(v: string): string {
  if (!v) return "";
  if (/^\d{4}-\d{2}-\d{2}$/.test(v)) return v;
  const t = new Date(v);
  return Number.isNaN(t.getTime()) ? "" : t.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/* toLocalInput is the reverse, for an <input type="datetime-local">. */
export function toLocalInput(v: string): string {
  if (!v) return "";
  if (/^\d{4}-\d{2}-\d{2}$/.test(v)) return `${v}T00:00`;
  const t = new Date(v);
  if (Number.isNaN(t.getTime())) return "";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())}T${p(t.getHours())}:${p(t.getMinutes())}`;
}
