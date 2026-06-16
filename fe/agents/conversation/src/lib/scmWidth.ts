export const SCM_MIN_W = 280;
export const SCM_MAX_W = 720;
export const SCM_DEFAULT_W = 384;
export const SCM_WIDTH_KEY = "wick.scm.width";

export function clampScmWidth(n: number): number {
  if (Number.isNaN(n)) return SCM_DEFAULT_W;
  return Math.min(SCM_MAX_W, Math.max(SCM_MIN_W, Math.round(n)));
}

export function readScmWidth(): number {
  try {
    if (typeof localStorage === "undefined") return SCM_DEFAULT_W;
    const raw = localStorage.getItem(SCM_WIDTH_KEY);
    if (raw === null) return SCM_DEFAULT_W;
    const n = parseInt(raw, 10);
    return Number.isNaN(n) ? SCM_DEFAULT_W : clampScmWidth(n);
  } catch {
    return SCM_DEFAULT_W;
  }
}

export function writeScmWidth(n: number): number {
  const w = clampScmWidth(n);
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(SCM_WIDTH_KEY, String(w));
  } catch {
    /* persistence unavailable — keep in-memory width */
  }
  return w;
}
