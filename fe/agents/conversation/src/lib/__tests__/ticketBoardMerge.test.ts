import { describe, test, expect } from "vitest";
import { buttonCaption, cardKey, groupBoardButtons, mergeBoards, statusesFor, unionStatuses, type BoardProject } from "../ticketBoardMerge.js";
import type { TicketBoard } from "../types/agents.js";

const board = (statuses: string[], buttons: { id?: string; label: string; placement?: "board" | "ticket" }[] = []): TicketBoard => ({
  config: { enabled: true, integrations: { buttons: buttons.map((b) => ({ url: "https://x", ...b })) } },
  statuses: statuses.map((key) => ({ key, label: key.toUpperCase() })),
  tickets: [],
  untracked: [],
});

describe("ticketBoardMerge", () => {
  test("unionStatuses keeps the first project's order, then extras", () => {
    const ps: BoardProject[] = [
      { id: "a", name: "A", board: board(["open", "doing", "done"]) },
      { id: "b", name: "B", board: board(["triage", "open", "done", "verify"]) },
    ];
    expect(unionStatuses(ps).map((s) => s.key)).toEqual(["open", "doing", "done", "triage", "verify"]);
  });

  test("groupBoardButtons counts projects per label and skips unsaved / ticket buttons", () => {
    const ps: BoardProject[] = [
      { id: "a", name: "A", board: board(["open"], [{ id: "1", label: "Sync", placement: "board" }, { label: "Sync", placement: "board" }, { id: "2", label: "Row", placement: "ticket" }]) },
      { id: "b", name: "B", board: board(["open"], [{ id: "3", label: "Sync", placement: "board" }]) },
    ];
    const g = groupBoardButtons(ps);
    expect(g.map(buttonCaption)).toEqual(["Sync · 2"]);
    expect(g[0].runs.map((r) => r.button.id)).toEqual(["1", "3"]);
  });

  test("statusesFor narrows to the project's own keys, sentinel when none", () => {
    const p: BoardProject = { id: "a", name: "A", board: board(["open", "done"]) };
    expect(statusesFor(["open", "review"], p, "\u0000none")).toEqual(["open"]);
    expect(statusesFor(["review"], p, "\u0000none")).toEqual(["\u0000none"]);
  });

  test("mergeBoards keeps one ticket id from two projects as two cards", () => {
    const t = (title: string) => ({ id: "T-4F2A", title, status: "open", sessions: 0, notes: 0, open_tasks: 0, updated_at: "", created_at: "", stale: false });
    const a = { ...board(["open"]), tickets: [t("Alpha")] };
    const b = { ...board(["open"]), tickets: [t("Beta")] };
    const { board: m } = mergeBoards([
      { id: "a", name: "A", board: a },
      { id: "b", name: "B", board: b },
    ]);
    expect(m.tickets.map((x) => [x.project_id, x.title])).toEqual([["a", "Alpha"], ["b", "Beta"]]);
    expect(new Set(m.tickets.map(cardKey)).size).toBe(2);
    expect(cardKey(m.tickets[0])).toBe("a/T-4F2A");
    // The source boards are left untouched.
    expect(a.tickets[0]).not.toHaveProperty("project_id");
  });
});
