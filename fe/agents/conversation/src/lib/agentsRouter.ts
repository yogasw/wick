import { writable, type Readable } from "svelte/store";
import { teamSettingsTabOf, type TeamSettingsTab } from "./teamSettingsTabs.js";
import { connTabOf, type ConnTab } from "./connectionTabs.js";

/* Client router for the Agents app (/team). It is a separate router from
   router.ts on purpose: that one owns /sessions/<id> and turns every
   unknown path into the session list, which is exactly wrong here. The
   app's URLs are real pages — pushState + popstate, so the browser's back
   button and a refresh both land where the user was:

     /team                               roster, Captain opened
     /team/<handle>                      that agent's main chat
     /team/<handle>?session=<id>         one of its other conversations
     /team/<handle>?panel=settings&tab=… Settings drawer over the chat
     /team/<handle>?panel=sessions       "Other chats" drawer
     /team/<handle>?panel=connections    Connections drawer
     /team/<handle>?panel=connections&conn=a2a
                                         …on that tab (slack|telegram|a2a|rest)
     /team/<handle>?panel=scheduled      Scheduled drawer
     /team/group/<group session id>      a group chat
     /team/group/<id>?panel=group-settings
                                         its Settings drawer
     /team/tickets                       Tickets: the selected projects' board
     /team/tickets/<project id>/<ticket id>
                                         one ticket on it
     /team/tickets/<project id>/<ticket id>/s/<session id>
                                         one of that ticket's chats, opened
                                         inside Team (a project chat too)
     /team/tickets/<project id>/s/<session id>
                                         a chat from the board's rail
     /team?panel=new                     the + Agent wizard
     /team?panel=new&project=<id>        the wizard converting that project
                                         ("Make this an agent…")
     /team[/<handle>]?panel=team-settings&tab=…
                                         Team settings drawer (the user's
                                         own Team, not one agent) */

/* "remote" is the A2A remote agent's own tab (remoteAgent.ts). */
export type SettingsTab = "persona" | "access" | "tools" | "skills" | "mention" | "captain" | "session" | "avatar" | "advanced" | "remote" | "sharing";
export const SETTINGS_TABS: SettingsTab[] = ["persona", "access", "tools", "skills", "mention", "captain", "session", "avatar", "advanced", "remote", "sharing"];

/** Old tab names that still open the right tab from a bookmark. */
const TAB_ALIASES: Record<string, SettingsTab> = { features: "tools" };

/** settingsTabOf resolves a `tab=` value; unknown → persona. */
export function settingsTabOf(t: string | null): SettingsTab {
  if (!t) return "persona";
  if ((SETTINGS_TABS as string[]).includes(t)) return t as SettingsTab;
  return TAB_ALIASES[t] ?? "persona";
}

export { teamSettingsTabOf, type TeamSettingsTab } from "./teamSettingsTabs.js";

export type AgentsPanel =
  | { kind: "settings"; tab: SettingsTab }
  | { kind: "team-settings"; tab: TeamSettingsTab }
  | { kind: "sessions" }
  | { kind: "connections"; conn?: ConnTab }
  | { kind: "scheduled" }
  | { kind: "group-settings" }
  | { kind: "new"; project?: string };

export type AgentsRoute = {
  /** Agent handle from the path; null = no agent named (roster root). */
  handle: string | null;
  /** A non-main conversation of that agent; null = its main chat. */
  session: string | null;
  panel: AgentsPanel | null;
  /** A group chat's session id (/team/group/<id>); then handle is null. */
  group?: string | null;
  /** The Tickets page (/team/tickets…); then handle and group are null. No
      project/ticket = the board, both = that ticket's page. */
  tickets?: TicketsRoute | null;
};

/** session: a chat opened from the Tickets page, drawn in place; back goes
    to its ticket (both ids) or to the board (project only). */
export type TicketsRoute = { project?: string; ticket?: string; session?: string };

const ROOT = "/team";

/** Path words the app owns under /team, which no agent handle may take.
    "tickets" and "group" are also reserved by the server (team.ValidateHandle). */
export const RESERVED_HANDLES: readonly string[] = ["tickets", "group", "all", "here", "channel"];

/** isReservedHandle says whether h is taken by the app or a group mention. */
export function isReservedHandle(h: string): boolean {
  return RESERVED_HANDLES.includes(h);
}

/** parseAgentsRoute reads one URL into a route. `base` is the tool prefix
    (data-base, e.g. "/tools/agents"). Anything it does not recognise falls
    back to the roster root rather than throwing — a stale bookmark should
    open the app, not a blank page. */
export function parseAgentsRoute(pathname: string, search: string, base: string): AgentsRoute {
  const prefix = base + ROOT;
  let rest = "";
  if (pathname.startsWith(prefix + "/")) rest = pathname.slice(prefix.length + 1);
  const segs = rest.split("/").filter(Boolean);
  const seg = segs[0] ?? "";
  let handle: string | null = null;
  let group: string | null = null;
  let tickets: TicketsRoute | null = null;
  // "group" and "tickets" are reserved handles (isReservedHandle).
  if (seg === "group" && segs[1]) {
    try {
      group = decodeURIComponent(segs[1]);
    } catch {
      group = null;
    }
  } else if (seg === "tickets") {
    // A ticket needs both ids; half of one (or a bad escape) is the board.
    // A chat rides after "s": <project>/<ticket>/s/<id> or <project>/s/<id>.
    tickets = {};
    try {
      if (segs.length === 5 && segs[3] === "s") {
        tickets = { project: decodeURIComponent(segs[1]), ticket: decodeURIComponent(segs[2]), session: decodeURIComponent(segs[4]) };
      } else if (segs.length === 4 && segs[2] === "s") {
        tickets = { project: decodeURIComponent(segs[1]), session: decodeURIComponent(segs[3]) };
      } else if (segs[1] && segs[2]) {
        tickets = { project: decodeURIComponent(segs[1]), ticket: decodeURIComponent(segs[2]) };
      }
    } catch {
      tickets = {};
    }
  } else if (seg) {
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
      panel = { kind: "settings", tab: settingsTabOf(q.get("tab")) };
      break;
    }
    case "team-settings":
      panel = { kind: "team-settings", tab: teamSettingsTabOf(q.get("tab")) };
      break;
    case "sessions":
      panel = { kind: "sessions" };
      break;
    case "connections": {
      const conn = connTabOf(q.get("conn"));
      panel = conn ? { kind: "connections", conn } : { kind: "connections" };
      break;
    }
    case "scheduled":
      panel = { kind: "scheduled" };
      break;
    case "group-settings":
      if (group) panel = { kind: "group-settings" };
      break;
    case "new":
      panel = q.get("project") ? { kind: "new", project: q.get("project")! } : { kind: "new" };
      break;
  }
  const session = handle ? q.get("session") || null : null;
  if (tickets) return { handle, session, panel, tickets };
  return group ? { handle, session, panel, group } : { handle, session, panel };
}

/** formatAgentsRoute is the inverse of parseAgentsRoute. */
export function formatAgentsRoute(r: AgentsRoute, base: string): string {
  let path = base + ROOT;
  if (r.tickets) {
    path += "/tickets";
    const t = r.tickets;
    if (t.project && (t.ticket || t.session)) path += "/" + encodeURIComponent(t.project);
    if (t.project && t.ticket) path += "/" + encodeURIComponent(t.ticket);
    if (t.project && t.session) path += "/s/" + encodeURIComponent(t.session);
  } else if (r.group) path += "/group/" + encodeURIComponent(r.group);
  else if (r.handle) path += "/" + encodeURIComponent(r.handle);
  const q = new URLSearchParams();
  if (!r.tickets && !r.group && r.handle && r.session) q.set("session", r.session);
  if (r.panel) {
    q.set("panel", r.panel.kind);
    if (r.panel.kind === "settings" || r.panel.kind === "team-settings") q.set("tab", r.panel.tab);
    if (r.panel.kind === "new" && r.panel.project) q.set("project", r.panel.project);
    if (r.panel.kind === "connections" && r.panel.conn) q.set("conn", r.panel.conn);
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
