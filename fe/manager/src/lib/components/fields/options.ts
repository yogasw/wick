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

/* The heading used for fields that declare no group= tag. Matches the templ
   defaultGroupTitle so an ungrouped page looks exactly as before grouping. */
export const DEFAULT_GROUP_TITLE = "Configuration";

/* A group's fields, split by render shape: simple fields go in the 2-col
   grid; kvlist / picker fields render as sub-blocks under the grid so a
   dependent picker stays inside its group card. Mirrors Go view.fieldGroup. */
export type FieldGroup = {
  title: string;
  desc: string;
  simple: ConfigField[];
  kvlists: ConfigField[];
  pickers: ConfigField[];
};

/* parseGroup splits a group= value into a section title and optional
   description. Grammar: "Title" or "Title|Description". Mirrors the Go
   view.parseGroup helper. */
export function parseGroup(raw: string | undefined): { title: string; desc: string } {
  const v = (raw ?? "").trim();
  if (!v) return { title: DEFAULT_GROUP_TITLE, desc: "" };
  const i = v.indexOf("|");
  if (i >= 0) return { title: v.slice(0, i).trim(), desc: v.slice(i + 1).trim() };
  return { title: v, desc: "" };
}

/* groupFields partitions ALL fields (simple / kvlist / picker) into titled
   cards by their group tag, preserving first-seen group order. Within a group
   each field type lands in its own slot so kvlist / picker fields render as
   sub-blocks under the simple-field grid. Ungrouped fields collapse into the
   default "Configuration" card at its first-seen position. The first non-empty
   description seen for a group wins. Mirrors the Go view.groupRows helper. */
export function groupFields(fields: ConfigField[]): FieldGroup[] {
  const idx = new Map<string, number>();
  const out: FieldGroup[] = [];
  for (const f of fields) {
    const { title, desc } = parseGroup(f.group);
    let i = idx.get(title);
    if (i === undefined) {
      i = out.length;
      idx.set(title, i);
      out.push({ title, desc, simple: [], kvlists: [], pickers: [] });
    } else if (out[i].desc === "" && desc !== "") {
      out[i].desc = desc;
    }
    if (f.type === "kvlist") out[i].kvlists.push(f);
    else if (f.type === "picker") out[i].pickers.push(f);
    else out[i].simple.push(f);
  }
  return out;
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

/* isFieldVisible is the cascading form: a field is visible only when its own
   visible_when rule matches AND the field it depends on is itself visible.
   This keeps a grandchild (reaction_channels) hidden when its grandparent
   (reaction_trigger_enabled) is off, even though the child's own dependency
   (reaction_channels_mode) still holds a matching value. fieldsByKey maps
   every field's key to the field. Cycle-guarded (fails open on a loop).
   Mirrors the templ applyVisibility cascade. */
export function isFieldVisible(
  field: ConfigField,
  fieldsByKey: Map<string, ConfigField>,
  values: Record<string, string>,
  seen: Set<string> = new Set(),
): boolean {
  const rule = field.visible_when;
  if (!rule) return true;
  if (!isVisible(rule, values)) return false;
  const i = rule.indexOf(":");
  if (i < 0) return true;
  const depKey = rule.slice(0, i).trim();
  const dep = fieldsByKey.get(depKey);
  if (!dep || dep === field || seen.has(field.key)) return true; // missing dep / self / cycle → fail open
  seen.add(field.key);
  return isFieldVisible(dep, fieldsByKey, values, seen);
}
