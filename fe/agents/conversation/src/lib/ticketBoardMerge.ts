import type { TicketBoard, TicketButton, TicketCard, TicketStatus } from "./types/agents.js";

/* The Team Tickets board is N project boards drawn as one. Each project
   keeps its own statuses, buttons and filter on the server; this file only
   decides how they meet on screen, so KanbanBoard's multi-project mode and
   its tests share one answer. */

/** One project's board as the combined board receives it. */
export type BoardProject = { id: string; name: string; board: TicketBoard };

/** unionStatuses is the combined board's columns: every status key of
    every project, in the first project's order, then each later project's
    extra keys in its own order. A key's label and terminal flag come from
    the first project that has it. */
export function unionStatuses(projects: readonly BoardProject[]): TicketStatus[] {
  const out: TicketStatus[] = [];
  const seen = new Set<string>();
  for (const p of projects) {
    for (const s of p.board.statuses) {
      if (seen.has(s.key)) continue;
      seen.add(s.key);
      out.push({ ...s });
    }
  }
  return out;
}

/** hasStatus says whether a project's board has a column with this key —
    a card may only be dropped where its own project has the status. */
export function hasStatus(p: BoardProject | undefined, key: string): boolean {
  return !!p && p.board.statuses.some((s) => s.key === key);
}

/** cardKey names a card on the combined board. Ticket ids are only unique
    within a project (two projects can both hold T-4F2A, and a synced id is
    the same everywhere), so the key carries the project too. A card with
    no project (the single board) is keyed by its id alone. */
export function cardKey(t: Pick<TicketCard, "id" | "project_id">): string {
  return t.project_id ? `${t.project_id}/${t.id}` : t.id;
}

/** mergeBoards folds the boards into one payload for the toolbar and the
    columns. Every card it emits carries its `project_id`, which is what
    PATCH, open, drop and per-card fields go by; session ids are global, so
    chats are mapped through `sessionProject`. `config` is the first
    project's. */
export function mergeBoards(projects: readonly BoardProject[]): {
  board: TicketBoard;
  sessionProject: Map<string, string>;
} {
  const sessionProject = new Map<string, string>();
  const board: TicketBoard = {
    config: projects[0]?.board.config ?? { enabled: true },
    tickets: [],
    untracked: [],
    untracked_total: 0,
    statuses: unionStatuses(projects),
    users: {},
    me: projects.find((p) => p.board.me)?.board.me,
  };
  for (const p of projects) {
    for (const t of p.board.tickets) {
      for (const r of t.session_rows ?? []) sessionProject.set(r.id, p.id);
      board.tickets.push({ ...t, project_id: p.id });
    }
    for (const r of p.board.untracked) {
      sessionProject.set(r.id, p.id);
      board.untracked.push(r);
    }
    board.untracked_total! += p.board.untracked_total ?? p.board.untracked.length;
    Object.assign(board.users!, p.board.users ?? {});
  }
  return { board, sessionProject };
}

/** A toolbar button of the combined board: one label, fired once per
    project that has a board button with that label. */
export type BoardButtonGroup = {
  label: string;
  runs: { projectId: string; projectName: string; button: TicketButton }[];
};

/** groupBoardButtons collects each project's saved board-placed buttons by
    label, in first-seen order. Only the projects passed in take part, so a
    project that is not selected (or filtered out) is never fired. */
export function groupBoardButtons(projects: readonly BoardProject[]): BoardButtonGroup[] {
  const groups: BoardButtonGroup[] = [];
  for (const p of projects) {
    for (const b of p.board.config?.integrations?.buttons ?? []) {
      if (b.placement !== "board" || (b.id ?? "") === "") continue;
      let g = groups.find((x) => x.label === b.label);
      if (!g) groups.push((g = { label: b.label, runs: [] }));
      // A project with two buttons of one label fires the first only.
      if (!g.runs.some((r) => r.projectId === p.id)) {
        g.runs.push({ projectId: p.id, projectName: p.name, button: b });
      }
    }
  }
  return groups;
}

/** buttonCaption is the grouped button's text: "Sync · 2" when more than
    one project runs it, the plain label otherwise. */
export function buttonCaption(g: BoardButtonGroup): string {
  return g.runs.length > 1 ? `${g.label} · ${g.runs.length}` : g.label;
}

/** statusesFor narrows the combined board's active columns to one
    project's own keys, for the filter a board button sends that project.
    Nothing left is spelled with the board's empty-selection sentinel, as an
    empty list reads as "no filter" on the wire. */
export function statusesFor(active: readonly string[], p: BoardProject, none: string): string[] {
  const own = active.filter((k) => hasStatus(p, k));
  return own.length > 0 ? own : [none];
}
