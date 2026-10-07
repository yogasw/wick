/* Picker path / pin grammar, mirror of internal/agents/provider/modelsets.go.

   A grouped picker level is addressed by a PATH of segments (wick: live set
   id; omp/opencode: provider, then account). On the wire the path is one
   string, each segment escaped and joined by "/"; a pin is "<path>@<model>".
   The path is escaped so it never holds a raw "@" — the FIRST "@" splits,
   and the model id after it may carry "/" or "@". wick's historical
   "<entry>@<model>" is the one-segment form, so old pins read unchanged. */

export function encodePath(path: string[]): string {
  return path.map((s) => encodeURIComponent(s)).join("/");
}

export function decodePath(s: string): string[] {
  return s
    .split("/")
    .filter((seg) => seg !== "")
    .map((seg) => {
      try {
        return decodeURIComponent(seg);
      } catch {
        return seg;
      }
    });
}

export function encodePin(path: string[], model: string): string {
  return path.length ? `${encodePath(path)}@${model}` : model;
}

/** Splits a pin; `grouped` is false for a flat model id (no "@"). */
export function decodePin(pin: string): { path: string[]; model: string; grouped: boolean } {
  const at = pin.indexOf("@");
  if (at < 0) return { path: [], model: pin, grouped: false };
  return { path: decodePath(pin.slice(0, at)), model: pin.slice(at + 1), grouped: true };
}
