/* ConfigField mirrors the Go entity.Config JSON shape (pkg/entity/config.go) —
   one editable row derived from a `wick:"..."`-tagged struct field. It's the
   generic contract between a BE that reflects a config/override struct and the
   ConfigForm renderer here. Only the fields ConfigForm actually reads are typed;
   the BE may send more (they're ignored). */
export type ConfigField = {
  Key: string;
  Value?: string;
  /** Widget: "checkbox"/"bool"/"boolean" (toggle), "dropdown", or text-ish. */
  Type?: string;
  /** Pipe-separated ("a|b|c") — dropdown option values. */
  Options?: string;
  Description?: string;
  Required?: boolean;
  /** "<field>:<value>" (or "<field>:a|b|c") — show only while it matches. */
  visible_when?: string;
  hidden?: boolean;
  /** Optional section heading; "Title" or "Title|Description". */
  Group?: string;
};

/** dropdownOptions splits a field's pipe-separated Options into a list. */
export function dropdownOptions(f: ConfigField): string[] {
  return (f.Options ?? "")
    .split("|")
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

/** isVisible evaluates a field's visible_when against the current value map. */
export function isVisible(f: ConfigField, values: Record<string, string>): boolean {
  if (f.hidden) return false;
  if (!f.visible_when) return true;
  const idx = f.visible_when.indexOf(":");
  if (idx < 0) return true;
  const key = f.visible_when.slice(0, idx).trim();
  const expected = f.visible_when.slice(idx + 1).trim();
  const allowed = expected.split("|").map((s) => s.trim());
  return allowed.includes(values[key] ?? "");
}

/** isToggle reports whether a field renders as an on/off switch. The Go tag
    reference treats bool/boolean/checkbox as aliases of the same control. */
export function isToggle(f: ConfigField): boolean {
  return f.Type === "checkbox" || f.Type === "bool" || f.Type === "boolean";
}
