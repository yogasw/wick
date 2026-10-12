import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const toastError = vi.fn();
vi.mock("@wick-fe/common-stores", async (orig) => ({
  ...(await orig<typeof import("@wick-fe/common-stores")>()),
  toastError: (...a: unknown[]) => toastError(...a),
}));

import KanbanBoard from "../KanbanBoard.svelte";
import type { TicketBoard, TicketCard } from "../../types/agents.js";
import type { BoardProject } from "../../ticketBoardMerge.js";

/* Multi-project mode (Team › Tickets). Same fetch stub as the single
   board's tests: every API call goes through it and is recorded. */
const calls: { method: string; url: string; body?: unknown }[] = [];

beforeEach(() => {
  calls.length = 0;
  toastError.mockReset();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(typeof input === "string" ? input : ((input as Request).url ?? input));
      let body: unknown;
      if (init?.body) {
        const b = init.body as ArrayBufferView | string;
        const raw =
          typeof b === "string"
            ? b
            : new TextDecoder().decode(
                ArrayBuffer.isView(b) ? new Uint8Array(b.buffer, b.byteOffset, b.byteLength) : b,
              );
        try {
          body = JSON.parse(raw);
        } catch {
          body = raw;
        }
      }
      calls.push({ method: (init?.method ?? "GET").toUpperCase(), url, body });
      const answer = url.includes("/board-actions/") ? { ok: true, status: 200, message: "queued" } : { status: "ok" };
      return new Response(JSON.stringify(answer), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
});

const card = (id: string, title: string, status: string): TicketCard => ({
  id,
  title,
  status,
  session_rows: [],
  sessions: 0,
  notes: 0,
  open_tasks: 0,
  updated_at: "2026-10-01T00:00:00Z",
  created_at: "2026-10-01T00:00:00Z",
  stale: false,
});

const alpha: TicketBoard = {
  config: {
    enabled: true,
    integrations: {
      buttons: [
        { id: "b-sync-a", label: "Sync", url: "https://a.example/sync", placement: "board" },
        { id: "b-row", label: "Row only", url: "https://a.example/row", placement: "ticket" },
      ],
    },
  },
  statuses: [
    { key: "open", label: "Open" },
    { key: "in_progress", label: "In Progress" },
    { key: "done", label: "Done", terminal: true },
  ],
  me: "u-me",
  users: { "u-me": "Me" },
  untracked: [{ id: "loose-a", label: "Alpha loose chat" }],
  untracked_total: 30,
  untracked_next: "cursor-a",
  tickets: [card("T-A1", "Alpha webhook", "open")],
};

const beta: TicketBoard = {
  config: {
    enabled: true,
    integrations: {
      buttons: [
        { id: "b-sync-b", label: "Sync", url: "https://b.example/sync", placement: "board" },
        { id: "b-export", label: "Export", url: "https://b.example/export", placement: "board" },
      ],
    },
  },
  // Its own stages: shares two keys with Alpha and adds "review".
  statuses: [
    { key: "open", label: "Open" },
    { key: "review", label: "Review" },
    { key: "done", label: "Done", terminal: true },
  ],
  me: "u-me",
  users: { "u-me": "Me", "u-2": "Other" },
  untracked: [{ id: "loose-b", label: "Beta loose chat" }],
  untracked_total: 1,
  tickets: [card("T-B1", "Beta invoice", "open")],
};

const projects = (): BoardProject[] => [
  { id: "p-a", name: "Alpha", board: alpha },
  { id: "p-b", name: "Beta", board: beta },
];

function renderMulti(overrides: Record<string, unknown> = {}) {
  const props = {
    base: "/tools/agents",
    projects: projects(),
    filter: {},
    untrackedOwner: "me" as const,
    onUntrackedOwner: vi.fn(),
    onUntrackedMore: vi.fn(),
    onFilter: vi.fn(),
    onOpen: vi.fn(),
    onOpenSession: vi.fn(),
    onReload: vi.fn(),
    ...overrides,
  };
  const utils = render(KanbanBoard, { props });
  return { ...utils, props };
}

function dt() {
  const store: Record<string, string> = {};
  return {
    setData: (k: string, v: string) => { store[k] = v; },
    getData: (k: string) => store[k] ?? "",
    effectAllowed: "",
    dropEffect: "",
  };
}

const columnLabels = (c: HTMLElement) =>
  [...c.querySelectorAll('[role="list"][aria-label]')].map((e) => e.getAttribute("aria-label"));

describe("KanbanBoard — several projects", () => {
  test("columns are the union of the projects' statuses, first project's order first", () => {
    const { container } = renderMulti();
    expect(columnLabels(container)).toEqual(["Open", "In Progress", "Done", "Review"]);
  });

  test("cards carry their project's name", () => {
    renderMulti();
    expect(screen.getByTestId("ticket-project-T-A1").textContent).toContain("📁 Alpha");
    expect(screen.getByTestId("ticket-project-T-B1").textContent).toContain("📁 Beta");
  });

  test("a card dropped on a column its project lacks is refused without a call", async () => {
    const { container } = renderMulti();
    const src = screen.getByTestId("ticket-card-T-A1");
    const review = container.querySelector('[aria-label="Review"]') as HTMLElement;
    const transfer = dt();
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    expect(review.dataset.refused).toBe("true");
    await fireEvent.dragOver(review, { dataTransfer: transfer });
    await fireEvent.drop(review, { dataTransfer: transfer });
    expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(0);
    expect(toastError).toHaveBeenCalledWith('Alpha has no "Review" column');
  });

  test("a card dropped on a column its project has moves it", async () => {
    const { container } = renderMulti();
    const src = screen.getByTestId("ticket-card-T-B1");
    const review = container.querySelector('[aria-label="Review"]') as HTMLElement;
    const transfer = dt();
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    expect(review.dataset.refused).toBeUndefined();
    await fireEvent.dragOver(review, { dataTransfer: transfer });
    await fireEvent.drop(review, { dataTransfer: transfer });
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH" && c.url.endsWith("/api/tickets/T-B1"))).toBe(true));
    expect(calls.find((c) => c.method === "PATCH")!.body).toEqual({ status: "review" });
  });

  test("board buttons are grouped by label with how many projects run them", () => {
    renderMulti();
    expect(screen.getByTestId("board-button-Sync").textContent).toBe("Sync · 2");
    expect(screen.getByTestId("board-button-Export").textContent).toBe("Export");
    expect(screen.queryByText("Row only")).toBeNull();
  });

  test("a grouped button fires once per project with that project's own columns", async () => {
    renderMulti();
    await fireEvent.click(screen.getByTestId("board-button-Sync"));
    await waitFor(() => expect(calls.filter((c) => c.url.includes("/board-actions/"))).toHaveLength(2));
    const fired = calls.filter((c) => c.url.includes("/board-actions/"));
    const a = fired.find((c) => c.url.includes("/api/projects/p-a/board-actions/b-sync-a"));
    const b = fired.find((c) => c.url.includes("/api/projects/p-b/board-actions/b-sync-b"));
    expect(a!.body).toEqual({ assignee: "", statuses: ["open", "in_progress", "done"] });
    expect(b!.body).toEqual({ assignee: "", statuses: ["open", "done", "review"] });
    // One result panel per project.
    expect(await screen.findByText("Sync · 📁 Alpha")).toBeTruthy();
    expect(await screen.findByText("Sync · 📁 Beta")).toBeTruthy();
  });

  test("a button only one project has fires only there", async () => {
    renderMulti();
    await fireEvent.click(screen.getByTestId("board-button-Export"));
    await waitFor(() => expect(calls.filter((c) => c.url.includes("/board-actions/"))).toHaveLength(1));
    expect(calls[0].url).toContain("/api/projects/p-b/board-actions/b-export");
  });

  test("the project select narrows the cards and the projects a button fires on", async () => {
    const { container } = renderMulti();
    const sel = screen.getByTestId("board-project") as HTMLSelectElement;
    expect(sel.options[0].textContent).toBe("Project: All (2)");
    await fireEvent.change(sel, { target: { value: "p-b" } });
    expect(screen.queryByTestId("ticket-title-T-A1")).toBeNull();
    expect(screen.getByTestId("ticket-title-T-B1")).toBeTruthy();
    expect(columnLabels(container)).toEqual(["Open", "Review", "Done"]);
    expect(screen.getByTestId("board-button-Sync").textContent).toBe("Sync");
    await fireEvent.click(screen.getByTestId("board-button-Sync"));
    await waitFor(() => expect(calls.filter((c) => c.url.includes("/board-actions/"))).toHaveLength(1));
    expect(calls[0].url).toContain("/api/projects/p-b/");
  });

  test("+ New ticket asks which project before it can add", async () => {
    renderMulti();
    await fireEvent.click(screen.getByRole("button", { name: "+ New ticket" }));
    const pick = screen.getByTestId("new-ticket-project") as HTMLSelectElement;
    await fireEvent.input(screen.getByLabelText("New ticket title"), { target: { value: "Ship it" } });
    const add = screen.getByRole("button", { name: "Add" }) as HTMLButtonElement;
    expect(add.disabled).toBe(true);
    await fireEvent.change(pick, { target: { value: "p-b" } });
    expect(add.disabled).toBe(false);
    await fireEvent.click(add);
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/api/projects/p-b/tickets"))).toBe(true));
  });

  test("opening a card names its project", async () => {
    const { props } = renderMulti();
    await fireEvent.click(screen.getByTestId("ticket-title-T-B1"));
    expect(props.onOpen).toHaveBeenCalledWith("T-B1", "p-b");
  });

  test("the untracked rail groups chats by project and pages each on its own", async () => {
    const { props } = renderMulti({ filter: { show_untracked: true } });
    expect(screen.getByTestId("rail-project-p-a").textContent).toContain("📁 Alpha");
    expect(screen.getByTestId("rail-project-p-a").textContent).toContain("1/30");
    expect(screen.getByTestId("rail-project-p-b").textContent).toContain("📁 Beta");
    expect(screen.queryByTestId("rail-more-p-b")).toBeNull();
    await fireEvent.click(screen.getByTestId("rail-more-p-a"));
    expect(props.onUntrackedMore).toHaveBeenCalledWith("p-a");
  });

  test("a chat cannot go on another project's ticket", async () => {
    renderMulti({ filter: { show_untracked: true } });
    const src = screen.getByTestId("untracked-loose-a");
    const target = screen.getByTestId("ticket-card-T-B1");
    const transfer = dt();
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    await fireEvent.dragOver(target, { dataTransfer: transfer });
    // Refused: no green "drop here" ring while hovering.
    expect(target.className).not.toContain("ring-green-500");
    await fireEvent.drop(target, { dataTransfer: transfer });
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    expect(toastError).toHaveBeenCalledWith("A chat can only go on a ticket of its own project");
  });

  test("with one project loaded, + New ticket does not ask and adds there", async () => {
    renderMulti({ projects: [projects()[1]] });
    await fireEvent.click(screen.getByRole("button", { name: "+ New ticket" }));
    expect(screen.queryByTestId("new-ticket-project")).toBeNull();
    await fireEvent.input(screen.getByLabelText("New ticket title"), { target: { value: "Solo" } });
    await fireEvent.click(screen.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/api/projects/p-b/tickets"))).toBe(true));
  });

  test("a move that empties a ticket names that ticket's project", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      new Response(JSON.stringify({ status: "ok", emptied_ticket: { id: "T-OLD", title: "Old" } }), { status: 200, headers: { "Content-Type": "application/json" } }),
    ));
    const onEmptied = vi.fn();
    renderMulti({ filter: { show_untracked: true }, onEmptied });
    const transfer = dt();
    await fireEvent.dragStart(screen.getByTestId("untracked-loose-a"), { dataTransfer: transfer });
    await fireEvent.dragOver(screen.getByTestId("ticket-card-T-A1"), { dataTransfer: transfer });
    await fireEvent.drop(screen.getByTestId("ticket-card-T-A1"), { dataTransfer: transfer });
    await waitFor(() => expect(onEmptied).toHaveBeenCalledWith({ id: "T-OLD", title: "Old" }, "p-a"));
  });

  test("a card with a room shows its members and Room · N members; Agent in room narrows the board", async () => {
    const withRoom = {
      ...card("T-R1", "Roomy", "open"),
      sessions: 2,
      room: {
        session_id: "room-1",
        members: [
          { kind: "project_default" as const, handle: "project", name: "Alpha" },
          { kind: "agent" as const, agent_id: "c-h", handle: "captain", name: "Captain", owner_id: "hana", owner_handle: "hana" },
        ],
      },
      session_rows: [{ id: "room-1", label: "Room · Roomy" }, { id: "chat-1", label: "One chat" }],
    };
    const ps = projects();
    ps[0] = { ...ps[0], board: { ...ps[0].board, tickets: [...ps[0].board.tickets, withRoom] } };
    renderMulti({ projects: ps });
    const row = screen.getByTestId("ticket-room-T-R1");
    expect(row.textContent).toContain("Room · 2 members");
    expect(screen.queryByTestId("session-row-room-1")).toBeNull();
    expect(screen.getByTestId("session-row-chat-1")).toBeTruthy();
    const sel = screen.getByTestId("board-room-agent") as HTMLSelectElement;
    expect([...sel.options].map((o) => o.textContent)).toEqual(["Agent in room: Any", "Agent in room: Alpha (project)", "Agent in room: Captain · @hana"]);
    await fireEvent.change(sel, { target: { value: "hana:c-h" } });
    await waitFor(() => expect(screen.queryByTestId("ticket-card-T-A1")).toBeNull());
    expect(screen.getByTestId("ticket-card-T-R1")).toBeTruthy();
  });

  test("no rooms on the board: no Agent in room select", () => {
    renderMulti();
    expect(screen.queryByTestId("board-room-agent")).toBeNull();
  });
});
