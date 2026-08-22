/* Rail layout preferences: which tabs sit in the visible strip, in what
   order, and how many fit before the rest fold into "More".

   The rail has outgrown a fixed strip — eight tabs today, more later — so
   the order is the user's to set and is remembered. Two rules shape the
   maths here:

   1. A tab carrying a badge (a count, or work in progress) is PROMOTED into
      the visible slots, because a number nobody can see is worse than a
      shifted position.
   2. Promotion never reorders what the user chose. A promoted tab jumps
      over quiet tabs but keeps its place relative to the other promoted
      ones, so "Context before Process" stays true whichever of them is
      currently loud. */

/** How many tabs the strip shows before the rest fold into "More".
 *
 * The default is deliberately at the ceiling: folding tabs away is a choice
 * the user makes when the rail gets long for THEM, not something that
 * happens on first visit to a rail they have not seen yet. Someone who
 * never opens the arrange panel keeps the full strip they had before this
 * existed. */
export const DEFAULT_VISIBLE = 12;
export const MIN_VISIBLE = 2;
export const MAX_VISIBLE = 12;

export type RailPrefs = {
  /** Tab ids in the user's chosen order. Ids absent here keep their
      built-in order behind the ones listed. */
  order: string[];
  /** How many to show before "More". */
  visible: number;
};

export const emptyRailPrefs: RailPrefs = { order: [], visible: DEFAULT_VISIBLE };

/** Clamps a stored count into range, tolerating junk from an older client. */
export function clampVisible(n: unknown): number {
  const v = typeof n === "number" && Number.isFinite(n) ? Math.round(n) : DEFAULT_VISIBLE;
  return Math.min(MAX_VISIBLE, Math.max(MIN_VISIBLE, v));
}

/** Normalises whatever came back from the profile into usable prefs. */
export function parseRailPrefs(raw: unknown): RailPrefs {
  const o = (raw ?? {}) as Partial<RailPrefs>;
  return {
    order: Array.isArray(o.order) ? o.order.filter((x) => typeof x === "string") : [],
    visible: clampVisible(o.visible),
  };
}

/** Applies the saved order to the tabs actually available right now.

    A saved order can name tabs that no longer exist (the Browser tab is
    hidden without a browser instance) and miss ones that do (a tab added
    by a later release). Both are tolerated: known ids lead in the saved
    order, unknown ones follow in their built-in order rather than
    disappearing. */
export function orderTabs<T extends { id: string }>(tabs: T[], order: string[]): T[] {
  const byId = new Map(tabs.map((t) => [t.id, t]));
  const out: T[] = [];
  for (const id of order) {
    const t = byId.get(id);
    if (t) {
      out.push(t);
      byId.delete(id);
    }
  }
  // Anything the saved order did not mention keeps its built-in position.
  for (const t of tabs) if (byId.has(t.id)) out.push(t);
  return out;
}

/** Splits ordered tabs into the visible strip and the overflow menu.

    `loud(id)` reports whether a tab currently carries a badge or is
    working. Loud tabs are pulled forward into the visible slots, keeping
    their relative order, so nothing with a count hides behind "More". */
export function splitRail<T extends { id: string }>(
  ordered: T[],
  visible: number,
  loud: (id: string) => boolean,
  /** The open panel's tab, if any. Always kept in the strip: a panel whose
      own tab vanished into "More" leaves nothing to click to close it. */
  activeId?: string | null,
): { shown: T[]; overflow: T[] } {
  const cap = clampVisible(visible);
  if (ordered.length <= cap) return { shown: ordered, overflow: [] };

  const isPromoted = (id: string) => loud(id) || id === activeId;
  const promoted = ordered.filter((t) => isPromoted(t.id));
  const quiet = ordered.filter((t) => !isPromoted(t.id));

  // Loud tabs first, then quiet ones fill whatever is left. Both keep the
  // order the user set — promotion changes which tabs are visible, never
  // how they are sequenced among themselves.
  const shownSet = new Set<string>();
  for (const t of promoted) {
    if (shownSet.size >= cap) break;
    shownSet.add(t.id);
  }
  for (const t of quiet) {
    if (shownSet.size >= cap) break;
    shownSet.add(t.id);
  }

  const shown = ordered.filter((t) => shownSet.has(t.id));
  const overflow = ordered.filter((t) => !shownSet.has(t.id));
  return { shown, overflow };
}

/** Moves one id within an order list, returning a new list.

    Reordering happens on the FULL tab list, not the visible slice: moving
    a tab in the overflow menu has to mean something, and a list that only
    knew about visible tabs could not express it. */
export function moveInOrder(order: string[], id: string, delta: number): string[] {
  const i = order.indexOf(id);
  if (i < 0) return order;
  const j = i + delta;
  if (j < 0 || j >= order.length) return order;
  const next = [...order];
  [next[i], next[j]] = [next[j], next[i]];
  return next;
}
