/* Rail layout preferences: which tabs sit in the strip, which are folded
   behind "More", and in what order.

   The rail has outgrown a fixed strip — eight tabs today, more later — so
   the layout is the user's to set and is remembered. Two rules shape it:

   1. A tab carrying a badge (a count, or work in progress) is PROMOTED into
      the strip even when folded, because a number nobody can see is worse
      than a shifted position.
   2. Promotion never reorders what the user chose. A promoted tab jumps
      over quiet tabs but keeps its place relative to the other promoted
      ones, so "Context before Process" stays true whichever of them is
      currently loud.

   Folding is per TAB, not a count. It used to be "show the first N" — but
   that is not the choice anyone is making: hiding Browser said nothing about
   how many tabs you want, and it moved whichever tab happened to sit at
   position N. Now `hidden` names the tabs, and the strip is everything else. */

/** The panels folded away on a rail nobody has arranged yet.
 *
 * A rail that ships every tab expanded runs the height of the window and
 * makes the user tidy up something they never asked for. But "keep the first
 * four" chose by POSITION, which is not the same as by usefulness — it folded
 * Source, the panel most likely to be wanted, purely for sitting last in the
 * built-in list.
 *
 * So the default names the quiet ones instead: panels that are situational,
 * or that announce themselves with a badge when they matter. Everything not
 * listed here starts in the strip, which also means a panel added by a later
 * release is visible rather than silently folded. */
export const DEFAULT_HIDDEN = ["workspace", "scheduled", "browser", "process"];

export type RailPrefs = {
  /** Tab ids in the user's chosen order. Ids absent here keep their
      built-in order behind the ones listed. */
  order: string[];
  /** Tab ids folded behind "More", or null when the user has never arranged
      the rail.

      The two are genuinely different and collapsing them would break one of
      them: null takes the default below, while an empty ARRAY is someone
      having deliberately unfolded everything — and that choice has to
      survive a reload rather than being re-folded as though untouched. */
  hidden: string[] | null;
};

export const emptyRailPrefs: RailPrefs = { order: [], hidden: null };

const ids = (v: unknown): string[] =>
  Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : [];

/** Normalises whatever came back from the profile into usable prefs.

    Tolerates the older `visible: N` shape by converting it: the first N tabs
    of the saved order stay in the strip and the rest are named as hidden, so
    an existing layout survives the change instead of resetting. */
export function parseRailPrefs(raw: unknown): RailPrefs {
  const o = (raw ?? {}) as Record<string, unknown>;
  const order = ids(o.order);
  if (!Array.isArray(o.hidden) && typeof o.visible === "number" && o.visible > 0) {
    return { order, hidden: order.slice(Math.round(o.visible)) };
  }
  return { order, hidden: Array.isArray(o.hidden) ? ids(o.hidden) : null };
}

/** The folded set to actually use, resolving "never arranged" to the default.

    Filtered against the tabs present right now, so the result never names a
    panel that does not exist — Browser needs a browser instance, and a
    conditional tab must not leave a phantom entry in the layout. */
export function resolveHidden(prefs: RailPrefs, ordered: { id: string }[]): string[] {
  if (prefs.hidden !== null) return prefs.hidden;
  const present = new Set(ordered.map((t) => t.id));
  return DEFAULT_HIDDEN.filter((id) => present.has(id));
}

/** The layout the server inlined into the page shell, if it did.

    The strip is drawn on the first frame, so fetching this meant painting
    the default and then collapsing to the saved value a request later —
    which reads as the setting not having stuck. The shell carries the same
    profile record instead, so the first paint is already right.

    Returns null when the attribute is absent or unusable, and the caller
    falls back to fetching: this is a delivery shortcut for one record, not a
    second place layouts live. Writes still go to the profile. */
export function railPrefsFromPage(el: HTMLElement | null): RailPrefs | null {
  const raw = el?.dataset.railPrefs;
  if (!raw) return null;
  try {
    return parseRailPrefs(JSON.parse(raw));
  } catch {
    return null;
  }
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

/** Splits ordered tabs into the strip and the overflow menu.

    `hidden` names the folded tabs. `loud(id)` reports whether a tab
    currently carries a badge or is working — a loud tab is pulled into the
    strip even while folded, so nothing with a count sits behind "More"
    unseen. That is a display override, not an edit: the tab stays folded in
    the saved layout and returns to "More" once it goes quiet. */
export function splitRail<T extends { id: string }>(
  ordered: T[],
  hidden: string[],
  loud: (id: string) => boolean,
  /** The open panel's tab, if any. Always kept in the strip: a panel whose
      own tab vanished into "More" leaves nothing to click to close it. */
  activeId?: string | null,
): { shown: T[]; overflow: T[] } {
  const folded = new Set(hidden);
  const inStrip = (id: string) => !folded.has(id) || loud(id) || id === activeId;
  return {
    shown: ordered.filter((t) => inStrip(t.id)),
    overflow: ordered.filter((t) => !inStrip(t.id)),
  };
}

/** Folds a tab away, or brings it back. */
export function toggleHidden(hidden: string[], id: string): string[] {
  return hidden.includes(id) ? hidden.filter((x) => x !== id) : [...hidden, id];
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

/** Moves one id to an absolute position, shifting the rest along.

    Dragging is not a series of swaps: dropping a tab four places up has to
    leave the tabs it passed in their old relative order, which a swap chain
    would scramble. And since "strip or More" is decided by position, this
    single operation is also how a tab is folded away or pulled back out —
    there is no separate hide.

    `to` is clamped, so a drop past either end lands at that end. */
export function reorderTo(order: string[], id: string, to: number): string[] {
  const i = order.indexOf(id);
  if (i < 0) return order;
  const j = Math.min(order.length - 1, Math.max(0, to));
  if (i === j) return order;
  const next = [...order];
  next.splice(i, 1);
  next.splice(j, 0, id);
  return next;
}
