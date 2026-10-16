import type { ProjectOption, TicketBoard, TicketFilter } from "./types/agents.js";
import { viewerId } from "./viewer.js";

/* The last ticket board each project answered, shared by Team › Tickets
   and a project's own board (Agents › Projects), so either one draws at
   once on open — a reload too — and refetches behind it
   (stale-while-revalidate). The fresh answer replaces the cached one; the
   stream's ticket signals keep refetching as before.

   Kept in memory and in sessionStorage under the reading user's id: a
   tab that logs in as somebody else never reads the last user's boards,
   and their entries are dropped on first use. Bounded: the newest
   BOARD_CACHE_MAX answers, none older than BOARD_CACHE_TTL_MS. With no
   known user nothing is stored at all. */

export const BOARD_CACHE_MAX = 12;
export const BOARD_CACHE_TTL_MS = 10 * 60_000;
const PREFIX = "wick.ticketBoards.";

type Entry = { at: number; board: TicketBoard };
type Saved = {
  boards: Record<string, Entry>;
  filters: Record<string, TicketFilter>;
  options?: ProjectOption[];
  /** Team settings' ticket_projects. */
  selection?: string[];
};

let mem: Saved | null = null;
let memUser = "";
/* Bumped by every local write (per project, or all of them at once): an
   answer to a request sent before the write may predate it, and must not
   land in the cache. */
const gens = new Map<string, number>();
let globalGen = 0;

const entryKey = (project: string, request: string) => project + "\u0000" + request;
const projectOf = (key: string) => key.slice(0, key.indexOf("\u0000"));

function storage(): Storage | null {
  try {
    return typeof sessionStorage === "undefined" ? null : sessionStorage;
  } catch {
    return null;
  }
}

function state(): Saved | null {
  const user = viewerId();
  if (!user) return null;
  if (mem && memUser === user) return mem;
  memUser = user;
  mem = { boards: {}, filters: {} };
  const st = storage();
  if (!st) return mem;
  try {
    // Another user's boards in this tab: never read, gone now.
    for (let i = st.length - 1; i >= 0; i--) {
      const k = st.key(i);
      if (k && k.startsWith(PREFIX) && k !== PREFIX + user) st.removeItem(k);
    }
    const raw = JSON.parse(st.getItem(PREFIX + user) ?? "null") as Saved | null;
    if (raw && typeof raw === "object") mem = { boards: raw.boards ?? {}, filters: raw.filters ?? {}, options: raw.options, selection: raw.selection };
  } catch {
    /* unreadable: start empty */
  }
  return mem;
}

function prune(s: Saved, now: number) {
  const live = Object.entries(s.boards)
    .filter(([, e]) => now - e.at <= BOARD_CACHE_TTL_MS)
    .sort((a, b) => b[1].at - a[1].at)
    .slice(0, BOARD_CACHE_MAX);
  s.boards = Object.fromEntries(live);
}

function persist(s: Saved) {
  const st = storage();
  if (!st) return;
  try {
    st.setItem(PREFIX + memUser, JSON.stringify(s));
  } catch {
    // Full: keep only the newest board and try once more.
    try {
      const newest = Object.entries(s.boards).sort((a, b) => b[1].at - a[1].at).slice(0, 1);
      st.setItem(PREFIX + memUser, JSON.stringify({ ...s, boards: Object.fromEntries(newest) }));
    } catch {
      /* storage off: memory still serves this page */
    }
  }
}

/** boardGen is a project's write counter; pass it back to rememberBoard
    with the answer of a request sent at that moment. */
export function boardGen(project: string): string {
  return globalGen + ":" + (gens.get(project) ?? 0);
}

/** cachedBoard answers the last board for this exact request, if fresh. */
export function cachedBoard(project: string, request: string, now = Date.now()): TicketBoard | undefined {
  const s = state();
  const e = s?.boards[entryKey(project, request)];
  if (!e || now - e.at > BOARD_CACHE_TTL_MS) return undefined;
  return e.board;
}

/** rememberBoard keeps an answer, unless a local write to the project
    happened after its request was sent (gen is boardGen from then). */
export function rememberBoard(project: string, request: string, board: TicketBoard, gen: string, now = Date.now()) {
  if (gen !== boardGen(project)) return;
  const s = state();
  if (!s) return;
  s.boards[entryKey(project, request)] = { at: now, board };
  prune(s, now);
  persist(s);
}

/** forgetBoards drops a project's cached boards (every project when none
    is named) after a local write: a status move, a new or deleted ticket. */
export function forgetBoards(project?: string) {
  if (project) gens.set(project, (gens.get(project) ?? 0) + 1);
  else globalGen++;
  const s = state();
  if (!s) return;
  for (const k of Object.keys(s.boards)) if (!project || projectOf(k) === project) delete s.boards[k];
  persist(s);
}

/** cachedFilter / rememberFilter keep the saved board filter for its key
    (a project id, or the combined board's key), so a board can send its
    request before the filter read comes back. */
export function cachedFilter(key: string): TicketFilter | undefined {
  return state()?.filters[key];
}
export function rememberFilter(key: string, f: TicketFilter) {
  const s = state();
  if (!s) return;
  s.filters[key] = f;
  persist(s);
}

/** cachedProjectOptions / rememberProjectOptions keep the Tickets picker's
    project list, which names the projects on the combined board. */
export function cachedProjectOptions(): ProjectOption[] | undefined {
  return state()?.options;
}
export function rememberProjectOptions(p: ProjectOption[]) {
  const s = state();
  if (!s) return;
  s.options = p;
  persist(s);
}

/** cachedTicketProjects / rememberTicketProjects keep the Tickets
    selection (Team settings), so the boards are asked for before the
    settings read comes back. */
export function cachedTicketProjects(): string[] | undefined {
  return state()?.selection;
}
export function rememberTicketProjects(ids: string[]) {
  const s = state();
  if (!s) return;
  s.selection = ids;
  persist(s);
}

/** Test hook: forget everything held in memory. */
export function resetBoardCache() {
  mem = null;
  memUser = "";
  gens.clear();
  globalGen = 0;
}
