/* Where the Team app's "Agents" switch goes: back to the Agents page the user came
   from — never another wick tool, since the switch reads as "the Agents space". The sidebar's "Team" link stores that page in sessionStorage under
   RETURN_KEY on click (layout.templ); other entries (the Overview card, a
   typed URL) fall back to document.referrer, and when neither is usable
   the conversation list. */

export const RETURN_KEY = "wick.team.return";

/** returnHref picks the "Agents" target. Only a same-origin path inside the Agents
    space (base) and outside the Team app counts — anything else (another site, "//host", "/\\host" which
    browsers read like "//host", the app itself) would make the link an open
    redirect or a loop. */
export function returnHref(stored: string | null, referrer: string, origin: string, base: string): string {
  const team = base + "/team";
  const within = (path: string, root: string) =>
    path.startsWith(root) && (path.length === root.length || "/?#".includes(path[root.length]));
  const ok = (path: string) => within(path, base) && !within(path, team);
  if (stored && stored[0] === "/" && stored[1] !== "/" && stored[1] !== "\\" && ok(stored)) return stored;
  if (referrer) {
    try {
      const u = new URL(referrer);
      if (u.origin === origin && ok(u.pathname)) return u.pathname + u.search + u.hash;
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

/** newSessionHref is the "New chat" target inside Agents: the landing marked
    with CLASSIC_VIEW, since the bare landing is what "Open Team when I open
    Agents" redirects to Team. */
export function newSessionHref(base: string): string {
  return classicHref(base + "/", base);
}

/** agentsHomeHref is where the Team app's "Agents" switch goes: the Agents
    home ("New session"), with the project of the Agents page the user came
    from picked when that page was a project. The new-session page only
    picks a project the user can still open, so a project they lost access
    to falls back to the default one instead of a page they would be stuck
    on. */
export function agentsHomeHref(stored: string | null, referrer: string, origin: string, base: string): string {
  const home = newSessionHref(base);
  const from = returnHref(stored, referrer, origin, base);
  const m = from.slice(base.length).match(/^\/projects\/([^/?#]+)/);
  if (!m) return home;
  let id = m[1];
  try {
    id = decodeURIComponent(id);
  } catch {
    // a malformed escape: pass the segment on as it is
  }
  return `${home}&project=${encodeURIComponent(id)}`;
}
