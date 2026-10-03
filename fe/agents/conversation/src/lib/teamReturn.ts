/* Where the Team app's "Switch to Agents" menu item goes: back to the wick page the user came
   from. The sidebar's "Team" link stores that page in sessionStorage under
   RETURN_KEY on click (layout.templ); other entries (the Overview card, a
   typed URL) fall back to document.referrer, and when neither is usable
   the conversation list. */

export const RETURN_KEY = "wick.team.return";

/** returnHref picks the "Agents" target. Only a same-origin path outside the Team
    app counts — anything else (another site, "//host", "/\\host" which
    browsers read like "//host", the app itself) would make the link an open
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

/** CLASSIC_VIEW is the query that keeps the wick Agents landing from
    sending the user straight back to Team ("Open Team when I open
    Agents"). */
export const CLASSIC_VIEW = "view=classic";

/** classicHref marks href with CLASSIC_VIEW when it is the Agents landing
    itself (base or base + "/", any query), so "Switch to Agents"
    cannot loop back into Team. Any other page is left as it is: only the
    bare landing redirects. */
export function classicHref(href: string, base: string): string {
  const cut = href.search(/[?#]/);
  const path = cut < 0 ? href : href.slice(0, cut);
  if (path !== base && path !== base + "/") return href;
  const rest = cut < 0 ? "" : href.slice(cut);
  const hash = rest.includes("#") ? rest.slice(rest.indexOf("#")) : "";
  const query = rest.startsWith("?") ? rest.slice(1, rest.length - hash.length) : "";
  if (query.split("&").includes(CLASSIC_VIEW)) return href;
  return `${path}?${query ? query + "&" : ""}${CLASSIC_VIEW}${hash}`;
}
