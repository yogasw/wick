// Shared rules for the Settings drawers' autosave (agent + Team).

/** AUTOSAVE_DELAY_MS coalesces a burst of clicks into one PATCH. */
export const AUTOSAVE_DELAY_MS = 400;
/** TEXT_DELAY_MS waits for typing to pause before saving a text field. */
export const TEXT_DELAY_MS = 800;
/** SLOW_SAVE_MS is when the footer says "Still saving…" instead of going quiet. */
export const SLOW_SAVE_MS = 10_000;

/**
 * sameValue compares two JSON-shaped values without caring about object
 * key order: the server may echo {kind,shape,color,expression} for a
 * {kind,shape,expression,color} write, and a JSON.stringify compare
 * would call that dirty forever. Keys holding undefined count as absent.
 * Array order still matters.
 */
export function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a === null || b === null || typeof a !== "object" || typeof b !== "object") return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((v, i) => sameValue(v, b[i]));
  }
  const ao = a as Record<string, unknown>;
  const bo = b as Record<string, unknown>;
  const keys = (o: Record<string, unknown>) => Object.keys(o).filter((k) => o[k] !== undefined);
  const ak = keys(ao);
  if (ak.length !== keys(bo).length) return false;
  return ak.every((k) => Object.prototype.hasOwnProperty.call(bo, k) && sameValue(ao[k], bo[k]));
}

/** patchKey is a stable fingerprint of a patch, independent of key order. */
export function patchKey(v: unknown): string {
  return JSON.stringify(v, (_k, val) =>
    val && typeof val === "object" && !Array.isArray(val)
      ? Object.fromEntries(Object.keys(val).sort().map((k) => [k, (val as Record<string, unknown>)[k]]))
      : val,
  );
}
