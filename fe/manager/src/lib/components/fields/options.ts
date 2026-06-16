import type { ConfigField } from "$lib/types.js";

export type OptPair = { label: string; value: string };

/* parseColOpts splits "label::value|label::value" into pairs. Entries with
   no "::" use the same string for both label and value. Mirrors the templ
   parseColOpts helper. */
export function parseColOpts(raw: string): OptPair[] {
  if (!raw) return [];
  return raw.split("|").map((p) => {
    const i = p.indexOf("::");
    if (i >= 0) return { label: p.slice(0, i), value: p.slice(i + 2) };
    return { label: p, value: p };
  });
}

/* kvColumns returns the column names for a kvlist field (from Options),
   falling back to ["value"] when Options is empty. */
export function kvColumns(field: ConfigField): string[] {
  if (!field.options) return ["value"];
  return field.options.split("|");
}

/* parseRows parses a JSON-array config value into row objects, tolerating
   empty or malformed input. */
export function parseRows(value: string): Record<string, string>[] {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value) as unknown;
    return Array.isArray(parsed) ? (parsed as Record<string, string>[]) : [];
  } catch {
    return [];
  }
}

/* isVisible evaluates a "<field>:<a|b|c>" visible_when predicate against the
   current values map. Empty rule = always visible. Mirrors the templ
   applyVisibility behaviour (pipe-separated OR on the value half). */
export function isVisible(rule: string, values: Record<string, string>): boolean {
  if (!rule) return true;
  const i = rule.indexOf(":");
  if (i < 0) return true;
  const key = rule.slice(0, i).trim();
  const allowed = rule
    .slice(i + 1)
    .split("|")
    .map((s) => s.trim());
  return allowed.includes(values[key] ?? "");
}
