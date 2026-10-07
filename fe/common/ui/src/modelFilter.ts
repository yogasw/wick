// The one model-filter grammar, mirrored from Go
// internal/agents/provider/modelfilter — change both together.
//   - whitespace-separated terms, ALL must hold (AND)
//   - a term is a case-insensitive substring of the model's id/label
//   - `a|b` inside a term = a OR b
//   - `!` / `-` prefix excludes: `!a|b` = neither a nor b
// e.g. `claude|gpt !mini` = (claude OR gpt) AND NOT mini. Blank = everything.
export const MODEL_FILTER_HELP = "space = AND, a|b = either, !term or -term excludes — e.g. claude|gpt !mini";

export function matchModelFilter(hay: string, query: string): boolean {
  const h = hay.toLowerCase();
  for (const raw of query.toLowerCase().split(/\s+/)) {
    let t = raw.trim();
    const exclude = t.startsWith("-") || t.startsWith("!");
    if (exclude) t = t.slice(1);
    const alts = t.split("|").filter((a) => a !== "");
    if (alts.length === 0) continue;
    const hit = alts.some((a) => h.includes(a));
    if (exclude === hit) return false;
  }
  return true;
}
