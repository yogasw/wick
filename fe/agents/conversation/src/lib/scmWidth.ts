/* Width of the conversation's side panel — source diffs, notes, context.
   One width for all of them, because the handle is on the panel and not on
   whatever happens to be inside it.

   The ceiling is generous: a long note or a wide diff is worth reading at
   full width, and the conversation beside it stays usable because the
   minimum is a fraction of any screen this layout appears on. */
export const SCM_MIN_W = 280;
export const SCM_MAX_W = 1100;
export const SCM_DEFAULT_W = 384;
export const SCM_WIDTH_KEY = "wick.scm.width";

/* The ceiling is also relative to the window: 1100px of panel on a 1280px
   screen would leave the conversation a gutter. Two thirds keeps the thread
   readable no matter how far the handle is dragged. */
export function maxScmWidth(): number {
  if (typeof window === "undefined" || !window.innerWidth) return SCM_MAX_W;
  return Math.max(SCM_MIN_W, Math.min(SCM_MAX_W, Math.round(window.innerWidth * 0.66)));
}

export function clampScmWidth(n: number): number {
  if (Number.isNaN(n)) return SCM_DEFAULT_W;
  return Math.min(maxScmWidth(), Math.max(SCM_MIN_W, Math.round(n)));
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
