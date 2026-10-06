/* Sizing helpers for the inline HTML-artifact iframe (HtmlArtifact.svelte),
   kept pure so they can be unit-tested without a browser.

   The iframe grows to the height its document reports, up to about the
   visible chat height (inlineCap). A document taller than that scrolls
   inside the frame instead of piling up in the chat — only then; content
   that fits never gets an inner scrollbar. The cap does not depend on the
   frame's own height, so a doc sized in 100vh simply settles at the cap
   instead of ratcheting. ⋮ → Full screen still shows it at full size.
   Two more things keep growth from moving the chat under the reader:
     - A remount (live turn → final turn, a re-render, Reload) used to start
       from the 320px default, so a tall preview collapsed and grew back. The
       last known height is remembered per artifact so a remount starts where
       the previous one ended.
     - The thread turns browser scroll anchoring off, so a preview that
       resizes while sitting above the visible area would shift everything
       below it. anchorShift() gives the scroll correction that cancels it. */

export const DEFAULT_HEIGHT = 320;
export const MAX_HEIGHT = 2400;

// Fraction of the visible chat height an inline preview may take before it
// scrolls internally — leaves room for the surrounding bubble and composer.
const VIEWPORT_RATIO = 0.8;

/** Tallest the inline iframe may get for a chat viewport of `viewport` px. */
export function inlineCap(viewport: number): number {
  if (!Number.isFinite(viewport) || viewport <= 0) return MAX_HEIGHT;
  return Math.min(MAX_HEIGHT, Math.max(DEFAULT_HEIGHT, Math.floor(viewport * VIEWPORT_RATIO)));
}

// How far below the cap a scrolling document must shrink before it stops
// scrolling. Turning the frame's scrollbar on narrows the document, and
// content whose height follows its width (a chart, an image, anything with an
// aspect ratio) then gets SHORTER — back under the cap, so the scrollbar goes,
// the content widens and is over the cap again. With the cap sitting in that
// band (a chat height that happens to land there, e.g. a composer grown to a
// few lines) the frame flipped every frame and visibly blinked. A margin wider
// than a scrollbar breaks the loop.
export const SCROLL_HYSTERESIS = 40;

/** Height to give the iframe for a reported document height, and whether the
    document overflows it (so the frame must scroll internally). `scrolling`
    is whether it scrolls now: a scrolling document stays scrolling at the cap
    until it is SCROLL_HYSTERESIS px under it. */
export function fitHeight(reported: number, viewport: number, scrolling = false): { height: number; scroll: boolean } {
  const cap = inlineCap(viewport);
  const h = Math.ceil(reported);
  if (h > cap) return { height: cap, scroll: true };
  if (scrolling && h > cap - SCROLL_HYSTERESIS) return { height: cap, scroll: true };
  return { height: h, scroll: false };
}

/** How far the chat scroller must move so a resize of an artifact that sits
    entirely ABOVE the visible area does not shift what the reader is looking
    at — the same idea as the history-prepend compensation. An artifact that
    is on screen grows below what is being read, so it needs none. */
export function anchorShift(prevHeight: number, nextHeight: number, frameBottom: number, viewTop: number): number {
  if (frameBottom > viewTop) return 0;
  return nextHeight - prevHeight;
}

const MEMO_LIMIT = 200;
const memo = new Map<string, number>();

/** Stable key for an artifact: its URL, or a hash of its inline source. */
export function artifactKey(url: string | undefined, src: string | null | undefined): string | null {
  if (url) return `u:${url}`;
  if (!src) return null;
  let h = 5381;
  for (let i = 0; i < src.length; i++) h = ((h << 5) + h + src.charCodeAt(i)) | 0;
  return `s:${src.length}:${h >>> 0}`;
}

export function rememberHeight(key: string | null, height: number): void {
  if (!key || !(height > 0)) return;
  memo.delete(key);
  memo.set(key, height);
  if (memo.size > MEMO_LIMIT) memo.delete(memo.keys().next().value as string);
}

export function recallHeight(key: string | null): number | undefined {
  return key ? memo.get(key) : undefined;
}

/** Test hook. */
export function _resetHeightMemo(): void {
  memo.clear();
}
