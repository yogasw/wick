import { writable, type Readable } from "svelte/store";

/* Client router for the Agents app (/team). It is a separate router from
   router.ts on purpose: that one owns /sessions/<id> and turns every
   unknown path into the session list, which is exactly wrong here. The
   app's URLs are real pages — pushState + popstate, so the browser's back
   button and a refresh both land where the user was:

     /team                               roster, Captain opened
     /team/<handle>                      that agent's main chat
     /team/<handle>?session=<id>         one of its other conversations
     /team/<handle>?panel=settings&tab=… Settings drawer over the chat
     /team/<handle>?panel=sessions       "Percakapan lain" drawer
     /team?panel=new                     the + Agent wizard */

export type SettingsTab = "persona" | "access" | "features" | "avatar";
export const SETTINGS_TABS: SettingsTab[] = ["persona", "access", "features", "avatar"];

export type AgentsPanel =
  | { kind: "settings"; tab: SettingsTab }
  | { kind: "sessions" }
  | { kind: "new" };

export type AgentsRoute = {
  /** Agent handle from the path; null = no agent named (roster root). */
  handle: string | null;
  /** A non-main conversation of that agent; null = its main chat. */
  session: string | null;
  panel: AgentsPanel | null;
};

const ROOT = "/team";

/** parseAgentsRoute reads one URL into a route. `base` is the tool prefix
    (data-base, e.g. "/tools/agents"). Anything it does not recognise falls
    back to the roster root rather than throwing — a stale bookmark should
    open the app, not a blank page. */
export function parseAgentsRoute(pathname: string, search: string, base: string): AgentsRoute {
  const prefix = base + ROOT;
  let rest = "";
  if (pathname.startsWith(prefix + "/")) rest = pathname.slice(prefix.length + 1);
  const seg = rest.split("/").filter(Boolean)[0] ?? "";
  let handle: string | null = null;
  if (seg) {
    try {
      handle = decodeURIComponent(seg);
    } catch {
      handle = null;
    }
  }

  const q = new URLSearchParams(search);
  let panel: AgentsPanel | null = null;
  switch (q.get("panel")) {
    case "settings": {
      const t = q.get("tab") as SettingsTab | null;
      panel = { kind: "settings", tab: t && SETTINGS_TABS.includes(t) ? t : "persona" };
      break;
    }
    case "sessions":
      panel = { kind: "sessions" };
      break;
    case "new":
      panel = { kind: "new" };
      break;
  }
  const session = handle ? q.get("session") || null : null;
  return { handle, session, panel };
}

/** formatAgentsRoute is the inverse of parseAgentsRoute. */
export function formatAgentsRoute(r: AgentsRoute, base: string): string {
  let path = base + ROOT;
  if (r.handle) path += "/" + encodeURIComponent(r.handle);
  const q = new URLSearchParams();
  if (r.handle && r.session) q.set("session", r.session);
  if (r.panel) {
    q.set("panel", r.panel.kind);
    if (r.panel.kind === "settings") q.set("tab", r.panel.tab);
  }
  const qs = q.toString();
  return qs ? `${path}?${qs}` : path;
}

function getBase(): string {
  return document.getElementById("app")?.dataset.base ?? "";
}

function current(): AgentsRoute {
  return parseAgentsRoute(window.location.pathname, window.location.search, getBase());
}

const _route = writable<AgentsRoute>(current());
let listening = false;

/** agentsRoute is the live route. Subscribing the first time installs the
    popstate listener, so importing this module in tests has no side effect
    on window until something actually reads the route. */
export const agentsRoute: Readable<AgentsRoute> = {
  subscribe(run, invalidate) {
    if (!listening) {
      listening = true;
      window.addEventListener("popstate", () => _route.set(current()));
      _route.set(current());
    }
    return _route.subscribe(run, invalidate);
  },
};

/** navigate moves the app to `next`. replace=true rewrites the current
    history entry instead — used when the app corrects the URL itself (e.g.
    /team resolving to the Captain) so back does not bounce through it. */
export function navigate(next: AgentsRoute, opts?: { replace?: boolean }): void {
  const url = formatAgentsRoute(next, getBase());
  const here = window.location.pathname + window.location.search;
  if (url !== here) {
    if (opts?.replace) history.replaceState({}, "", url);
    else history.pushState({}, "", url);
  }
  _route.set(current());
}
