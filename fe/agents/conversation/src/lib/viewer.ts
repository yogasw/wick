/**
 * Who is reading this page.
 *
 * The conversation shows messages from several people — your own, a
 * colleague's from the same dashboard, someone's from Slack. Telling them
 * apart needs one fact the turns themselves do not carry: which of those
 * people is you. The Go shell inlines it as `data-viewer-id` on #app so the
 * first paint is already correct, rather than every bubble briefly claiming
 * to be someone else's while a fetch resolves.
 *
 * Read from the DOM on first use and then cached: the logged-in user cannot
 * change without a page load, so re-reading per message would be pure
 * overhead in a long thread. Lazy rather than at module load so importing
 * this file never depends on #app already existing — which is also what lets
 * a test set a viewer before rendering.
 */
let cached: string | null = null;

/** viewerId returns the wick user id reading the page, or "" when unknown. */
export function viewerId(): string {
  if (cached === null) {
    cached =
      typeof document !== "undefined"
        ? (document.getElementById("app")?.dataset.viewerId ?? "").trim()
        : "";
  }
  return cached;
}

/**
 * setViewerId overrides the cached value. Only for tests — production reads
 * the id the Go shell inlined.
 */
export function setViewerId(id: string): void {
  cached = id;
}

/**
 * isViewer reports whether a sender's wick user id is the person reading.
 *
 * Compares the WICK user id rather than the platform id, because the same
 * human is `U0104` in Slack and a uuid in the dashboard — matching on the
 * platform id would label their own Slack messages as somebody else's.
 *
 * Unknown on either side returns false, which is the safe direction: an
 * unattributed turn shows a name rather than silently passing as yours.
 */
export function isViewer(wickUserID: string | undefined): boolean {
  const me = viewerId();
  if (!me || !wickUserID) return false;
  return wickUserID === me;
}
