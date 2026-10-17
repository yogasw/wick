import type { AgentItem, GroupItem, TeamFolder, TeamFolders } from "./api/team.js";
import type { RosterEntry } from "./rosterList.js";
import { dozing } from "./rosterList.js";
import type { RosterStatus } from "./rosterStatus.js";

/* Team-sidebar folders (team-groups-v2 mockup): the viewer's own folders
   of agents, kept apart from group CHATS (GroupItem), which stay loose in
   the list. Agents sort by tier inside and outside folders; folders keep
   the order the viewer set and only their icon changes. Pure, so the
   order, the icon state and the placement of shared agents are tested
   rather than buried in the template. */

/** Tier of an agent row: 0 working, 1 active (waits on someone, unread or
    awake today), 2 idle (dozing). */
export type Tier = 0 | 1 | 2;

type Status = Pick<RosterStatus, "work" | "attention" | "unread">;
type Dozer = Pick<AgentItem, "last_active" | "last_any_active" | "disabled">;

const ts = (raw: string | null | undefined) => (raw ? Date.parse(raw) || 0 : 0);

export function agentTier(a: Dozer, st: Status, nowMs: number = Date.now()): Tier {
  if (st.work !== "idle") return 0;
  return st.attention || st.unread || !dozing(a, st, nowMs) ? 1 : 2;
}

/** groupTier is where a group chat sits among loose agents: it never
    works, so unread or active today is tier 1, else idle. */
export function groupTier(g: Pick<GroupItem, "unread" | "last_active">, nowMs: number = Date.now()): Tier {
  if (g.unread) return 1;
  const today = new Date(nowMs);
  today.setHours(0, 0, 0, 0);
  return ts(g.last_active) >= today.getTime() ? 1 : 2;
}

/** byTier sorts entries by tier, then newest last_active first, ties by
    name; the Captain stays pinned on top. */
export function byTier(entries: RosterEntry[], statusOf: (a: AgentItem) => Status, nowMs: number = Date.now()): RosterEntry[] {
  const tier = (e: RosterEntry) => (e.kind === "agent" ? agentTier(e.agent, statusOf(e.agent), nowMs) : groupTier(e.group, nowMs));
  const at = (e: RosterEntry) => ts(e.kind === "agent" ? e.agent.last_active : e.group.last_active);
  const name = (e: RosterEntry) => (e.kind === "agent" ? e.agent.name : e.group.name);
  const captain = (e: RosterEntry) => (e.kind === "agent" && e.agent.is_captain ? 1 : 0);
  const keyed = entries.map((e) => ({ e, t: tier(e) }));
  keyed.sort((x, y) => captain(y.e) - captain(x.e) || x.t - y.t || at(y.e) - at(x.e) || name(x.e).localeCompare(name(y.e)));
  return keyed.map((k) => k.e);
}

/** The id an inherited folder (a shared agent's owner keeps it in a folder
    the viewer has no folder of that name for) goes by until it is saved. */
/** foldersOf is a GET/PUT answer (or a cached one) with every field
    present: an older server or a partial cache leaves some out. */
export function foldersOf(r: Partial<TeamFolders>): TeamFolders {
  return { folders: r.folders ?? [], place: r.place ?? {}, inherited: r.inherited ?? {} };
}

/** parseFoldersCache reads the folders the last open stored, for the
    roster's first paint; null when there is none or it is not folders. */
export function parseFoldersCache(raw: string | null): TeamFolders | null {
  try {
    const c = JSON.parse(raw ?? "null") as Partial<TeamFolders> | null;
    if (!c || typeof c !== "object" || !Array.isArray(c.folders)) return null;
    return foldersOf(c);
  } catch {
    return null;
  }
}

/** Roster rows move (flip, crossfade, a folder opening) for this long;
    not at all under prefers-reduced-motion or while a search filters. */
export const ROSTER_MOTION_MS = 180;
export function rosterMotionMs(reduced: boolean, searching: boolean): number {
  return reduced || searching ? 0 : ROSTER_MOTION_MS;
}

export const INHERITED_PREFIX = "inherited:";
export const isInheritedFolder = (id: string) => id.startsWith(INHERITED_PREFIX);

const nameKey = (n: string) => n.trim().toLowerCase();

/** Resolved is the sidebar's view of a TeamFolders: every folder to show
    (the viewer's in their order, then inherited ones by name) and the
    folder each filed agent sits in. */
export type Resolved = { folders: TeamFolder[]; placeOf: Record<string, string> };

/** resolveFolders places each agent: the viewer's own placement wins;
    else a shared agent goes to the viewer's folder named as its owner's,
    made up client-side (INHERITED_PREFIX id) when the viewer has none.
    A placement into a folder that is gone counts as loose. */
export function resolveFolders(s: TeamFolders | null, agentIds: string[]): Resolved {
  if (!s) return { folders: [], placeOf: {} };
  const folders = [...s.folders];
  const ids = new Set(folders.map((f) => f.id));
  const byName = new Map(folders.map((f) => [nameKey(f.name), f.id]));
  const placeOf: Record<string, string> = {};
  const made: TeamFolder[] = [];
  for (const id of agentIds) {
    const mine = s.place[id];
    if (mine !== undefined) {
      if (ids.has(mine)) placeOf[id] = mine;
      continue;
    }
    const n = (s.inherited ?? {})[id]?.trim();
    if (!n) continue;
    let fid = byName.get(nameKey(n));
    if (!fid) {
      fid = INHERITED_PREFIX + nameKey(n);
      byName.set(nameKey(n), fid);
      made.push({ id: fid, name: n });
    }
    placeOf[id] = fid;
  }
  made.sort((x, y) => x.name.localeCompare(y.name));
  return { folders: [...folders, ...made], placeOf };
}

/** newFolderId is a fresh folder id (the server only asks for ≤64 chars). */
export function newFolderId(): string {
  return `f${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
}

/** materialize is the PUT body for a change: inherited folders the change
    touched or that still hold an agent become real folders (fresh id),
    so a recipient's first change keeps what they were looking at; their
    agents follow by name, not by a placement, so the owner's later moves
    still show. place keeps only the viewer's own moves. */
export function materialize(s: TeamFolders, agentIds: string[], idOf: () => string = newFolderId): { folders: TeamFolder[]; place: Record<string, string> } {
  const r = resolveFolders(s, agentIds);
  const used = new Set(Object.values(r.placeOf));
  const swap = new Map<string, string>();
  const folders: TeamFolder[] = [];
  for (const f of r.folders) {
    if (!isInheritedFolder(f.id)) folders.push(f);
    else if (used.has(f.id) || Object.values(s.place).includes(f.id)) {
      const id = idOf();
      swap.set(f.id, id);
      folders.push({ ...f, id });
    }
  }
  const place: Record<string, string> = {};
  for (const [a, f] of Object.entries(s.place)) {
    const id = swap.get(f) ?? f;
    if (folders.some((d) => d.id === id)) place[a] = id;
  }
  return { folders, place };
}

/* Edits. Each returns a new TeamFolders and works on an inherited folder
   too: it is first copied into the viewer's own list under its made-up id
   (materialize gives it a real one on save). */

function own(s: TeamFolders, agentIds: string[], id: string): TeamFolders {
  if (!isInheritedFolder(id) || s.folders.some((f) => f.id === id)) return s;
  const f = resolveFolders(s, agentIds).folders.find((d) => d.id === id);
  return f ? { ...s, folders: [...s.folders, { ...f }] } : s;
}

export function setCollapsed(s: TeamFolders, agentIds: string[], id: string, collapsed: boolean): TeamFolders {
  const o = own(s, agentIds, id);
  return { ...o, folders: o.folders.map((f) => (f.id === id ? { ...f, collapsed } : f)) };
}

/** renameError is why a folder can't take a name ("" = it can): empty,
    too long, or another folder has it. */
export function renameError(s: TeamFolders, agentIds: string[], id: string, name: string): string {
  const n = name.trim();
  if (!n) return "Folder name can't be empty.";
  if ([...n].length > 40) return "Folder name is at most 40 characters.";
  const clash = resolveFolders(s, agentIds).folders.some((f) => f.id !== id && nameKey(f.name) === nameKey(n));
  return clash ? "A folder with that name already exists." : "";
}

/** renameFolder also pins the shared agents that sat in it by name, so
    they move with the new name instead of falling back to the old one. */
export function renameFolder(s: TeamFolders, agentIds: string[], id: string, name: string): TeamFolders {
  const o = own(s, agentIds, id);
  const place = { ...o.place };
  for (const [a, f] of Object.entries(resolveFolders(o, agentIds).placeOf)) if (f === id) place[a] = id;
  return { ...o, place, folders: o.folders.map((f) => (f.id === id ? { ...f, name: name.trim() } : f)) };
}

/** deleteFolder drops a folder; the agents in it become loose. */
export function deleteFolder(s: TeamFolders, id: string): TeamFolders {
  const place = Object.fromEntries(Object.entries(s.place).filter(([, f]) => f !== id));
  return { ...s, folders: s.folders.filter((f) => f.id !== id), place };
}

/** createFolder adds a folder at the end; returns it with the new state. */
export function createFolder(s: TeamFolders, name: string, idOf: () => string = newFolderId): { state: TeamFolders; id: string } {
  const id = idOf();
  return { state: { ...s, folders: [...s.folders, { id, name: name.trim() }] }, id };
}

/** moveAgent files an agent (folder "" = no folder). Moving a shared
    agent overrides where its owner keeps it. */
export function moveAgent(s: TeamFolders, agentIds: string[], agentId: string, folderId: string): TeamFolders {
  const o = folderId ? own(s, agentIds, folderId) : s;
  const place = { ...o.place };
  if (folderId) place[agentId] = folderId;
  else delete place[agentId];
  return { ...o, place };
}

/** canUnfile is whether "No folder" sticks: a shared agent shown in its
    owner's folder (inherited) follows that folder, and the server keeps no
    "loose" override, so only a folder of the viewer's own moves it. */
export function canUnfile(s: TeamFolders | null, agentId: string): boolean {
  return !s || !(s.inherited ?? {})[agentId];
}

/** reorderFolder moves folder `drag` before (or after) folder `target`. */
export function reorderFolder(s: TeamFolders, agentIds: string[], drag: string, target: string, after: boolean): TeamFolders {
  if (drag === target) return s;
  const o = own(own(s, agentIds, drag), agentIds, target);
  const moving = o.folders.find((f) => f.id === drag);
  if (!moving) return s;
  const rest = o.folders.filter((f) => f.id !== drag);
  const at = rest.findIndex((f) => f.id === target);
  if (at < 0) return s;
  rest.splice(at + (after ? 1 : 0), 0, moving);
  return { ...o, folders: rest };
}

/** The folder icon's state: "thinking" when any member works on its own,
    "waiting" when the only work is waiting on teammates (work "team");
    thinking beats waiting. */
export type FolderBusy = "thinking" | "waiting" | "";

export type FolderView = {
  folder: TeamFolder;
  agents: AgentItem[];
  busy: FolderBusy;
  /** The members in the busy state, for the badge count and summary. */
  busyAgents: AgentItem[];
  unread: number;
  summary: string;
  /** Every member dozes (or there are none): the row fades like one. */
  sleepy: boolean;
  last_active: string | null;
};

/** whoLine is "Paragon, KPJ thinking…": two names, then +N. */
export function whoLine(list: Pick<AgentItem, "name">[], verb: string): string {
  const names = list.slice(0, 2).map((a) => a.name).join(", ");
  return `${names}${list.length > 2 ? ` +${list.length - 2}` : ""} ${verb}…`;
}

export function folderView(folder: TeamFolder, members: AgentItem[], statusOf: (a: AgentItem) => Status, nowMs: number = Date.now()): FolderView {
  const sorted = byTier(members.map((a) => ({ kind: "agent" as const, id: a.id, agent: a })), statusOf, nowMs).map((e) => (e as { agent: AgentItem }).agent);
  const thinking = sorted.filter((a) => { const w = statusOf(a).work; return w !== "idle" && w !== "team"; });
  const waiting = sorted.filter((a) => statusOf(a).work === "team");
  const busy: FolderBusy = thinking.length ? "thinking" : waiting.length ? "waiting" : "";
  const busyAgents = busy === "thinking" ? thinking : busy === "waiting" ? waiting : [];
  const unread = sorted.reduce((n, a) => n + (statusOf(a).unread ? Math.max(1, a.unread_count ?? 0) : 0), 0);
  const summary =
    busy === "thinking" ? whoLine(thinking, "thinking") : busy === "waiting" ? whoLine(waiting, "waiting") : sorted.length === 1 ? "1 agent" : sorted.length ? `${sorted.length} agents` : "Empty";
  const last = sorted.reduce<string | null>((m, a) => (ts(a.last_active) > ts(m) ? a.last_active : m), null);
  return {
    folder,
    agents: sorted,
    busy,
    busyAgents,
    unread,
    summary,
    sleepy: sorted.every((a) => agentTier(a, statusOf(a), nowMs) === 2),
    last_active: last,
  };
}

/** teamList splits the roster (rosterEntries, already filtered by the
    search) into the loose rows on top and the folders under them. While
    searching, a folder without a match is left out. */
export function teamList(
  entries: RosterEntry[],
  r: Resolved,
  statusOf: (a: AgentItem) => Status,
  searching: boolean,
  nowMs: number = Date.now(),
): { loose: RosterEntry[]; folders: FolderView[] } {
  const loose: RosterEntry[] = [];
  const by = new Map<string, AgentItem[]>();
  for (const e of entries) {
    const f = e.kind === "agent" ? r.placeOf[e.agent.id] : undefined;
    if (f) by.set(f, [...(by.get(f) ?? []), (e as { agent: AgentItem }).agent]);
    else loose.push(e);
  }
  const folders = r.folders
    .filter((f) => !searching || by.has(f.id))
    .map((f) => folderView(f, by.get(f.id) ?? [], statusOf, nowMs));
  return { loose: byTier(loose, statusOf, nowMs), folders };
}
