/* Per-browser memory of which Detail sections are open. One key per
   section; a missing or unreadable entry falls back to the default. */

const PREFIX = "wick.providers.section.";

export function loadOpen(key: string, dflt: boolean): boolean {
  try {
    const v = globalThis.localStorage?.getItem(PREFIX + key);
    if (v === "1") return true;
    if (v === "0") return false;
  } catch {
    /* storage disabled: default */
  }
  return dflt;
}

export function saveOpen(key: string, open: boolean): void {
  try {
    globalThis.localStorage?.setItem(PREFIX + key, open ? "1" : "0");
  } catch {
    /* storage disabled: state lives for this page only */
  }
}

/* loadAllOpen returns every remembered section under group (keys
   "<group>.<section>"), for pages that keep one open-map. */
export function loadAllOpen(group: string): Record<string, boolean> {
  const out: Record<string, boolean> = {};
  try {
    const ls = globalThis.localStorage;
    if (!ls) return out;
    const pre = PREFIX + group + ".";
    for (let i = 0; i < ls.length; i++) {
      const k = ls.key(i);
      if (k && k.startsWith(pre)) out[k.slice(pre.length)] = ls.getItem(k) === "1";
    }
  } catch {
    /* storage disabled */
  }
  return out;
}
