// Plain-text schema renderer for the INPUT/OUTPUT Schema tab. Mirrors
// inferSchema() in the legacy editor.js — shows the shape of a value
// without the actual data, so operators can see what fields exist on
// an unfamiliar payload at a glance.
//
// Example output:
//   {
//     row: {
//       id: number,
//       name: string,
//     },
//     count: number,
//   }
export function inferSchema(v: unknown, depth = 0): string {
  if (v === null) return "null";
  if (Array.isArray(v)) {
    if (v.length === 0) return "array<unknown>";
    return "array<" + inferSchema(v[0], depth + 1) + ">";
  }
  if (typeof v === "object") {
    const obj = v as Record<string, unknown>;
    const entries = Object.entries(obj);
    if (entries.length === 0) return "{}";
    const pad = "  ".repeat(depth);
    const inner = entries
      .map(([k, val]) => `  ${pad}${k}: ${inferSchema(val, depth + 1)}`)
      .join(",\n");
    return "{\n" + inner + "\n" + pad + "}";
  }
  return typeof v;
}
