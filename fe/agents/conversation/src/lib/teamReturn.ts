/* Where the Team app's ↩ button goes: back to the wick page the user came
   from. The sidebar's "Team" link stores that page in sessionStorage under
   RETURN_KEY on click (layout.templ); other entries (the Overview card, a
   typed URL) fall back to document.referrer, and when neither is usable
   the conversation list. */

export const RETURN_KEY = "wick.team.return";

/** returnHref picks the ↩ target. Only a same-origin path outside the Team
    app counts — anything else (another site, "//host", "/\\host" which
    browsers read like "//host", the app itself) would make ↩ an open
    redirect or a loop. */
export function returnHref(stored: string | null, referrer: string, origin: string, base: string): string {
  const team = base + "/team";
  const inside = (path: string) =>
    path.startsWith(team) && (path.length === team.length || "/?#".includes(path[team.length]));
  if (stored && stored[0] === "/" && stored[1] !== "/" && stored[1] !== "\\" && !inside(stored)) return stored;
  if (referrer) {
    try {
      const u = new URL(referrer);
      if (u.origin === origin && !inside(u.pathname)) return u.pathname + u.search + u.hash;
    } catch {
      // not a URL: ignore
    }
  }
  return base + "/sessions";
}
