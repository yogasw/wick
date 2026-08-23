import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import KanbanBoard from "../KanbanBoard.svelte";
import type { TicketBoard } from "../../types/agents.js";

/* Attach/move/create go through the shared Effect client, which runs on
   fetch, so stubbing fetch drives the whole path. */
const calls: { method: string; url: string; body?: unknown }[] = [];

beforeEach(() => {
  calls.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(typeof input === "string" ? input : ((input as Request).url ?? input));
      // The client sends bodies as bytes, and jsdom's Uint8Array belongs to
      // another realm — decode by shape, not by instanceof.
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
      return new Response(JSON.stringify({ status: "ok" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

const board: TicketBoard = {
  config: {
    enabled: true,
    fields: [{ key: "priority", label: "Priority", type: "select", options: ["low", "high"] }],
  },
  statuses: [
    { key: "open", label: "Open" },
    { key: "in_progress", label: "In Progress" },
    { key: "waiting", label: "Waiting" },
    { key: "done", label: "Done", terminal: true },
  ],
  me: "u-me",
  users: { "u-me": "Me Myself", "u-2": "Other Person" },
  untracked: [
    { id: "loose-1", label: "Quick question about exports", lifecycle: "", last_active: "2026-08-22T05:00:00Z" },
  ],
  // The server sent one page of a larger set.
  untracked_total: 42,
  tickets: [
    {
      id: "T-4F2A",
      title: "Fix the webhook",
      status: "open",
      assignee: "u-me",
      fields: { priority: "high" },
      session_rows: [
        { id: "sess-a", label: "First attempt", lifecycle: "working" },
        { id: "sess-b", label: "Second attempt" },
      ],
      sessions: 2,
      notes: 2,
      open_tasks: 1,
      updated_at: "2026-08-22T00:00:00Z",
      created_at: "2026-08-20T00:00:00Z",
      stale: true,
    },
    {
      id: "T-99ZZ",
      title: "Old finished thing",
      status: "done",
      assignee: "u-2",
      session_rows: [],
      sessions: 0,
      notes: 0,
      open_tasks: 0,
      updated_at: "2026-08-01T00:00:00Z",
      created_at: "2026-07-30T00:00:00Z",
      stale: false,
    },
  ],
};

function renderBoard(overrides: Record<string, unknown> = {}) {
  const onFilter = vi.fn();
  const onOpen = vi.fn();
  const onOpenSession = vi.fn();
  const onReload = vi.fn();
  const utils = render(KanbanBoard, {
    props: {
      base: "/tools/agents",
      projectId: "p1",
      board,
      filter: {},
      onFilter,
      onOpen,
      onOpenSession,
      onReload,
      ...overrides,
    },
  });
  return { ...utils, onFilter, onOpen, onOpenSession, onReload };
}

/* The rail is opt-in, so anything testing it has to ask for it — the same
   filter flag that makes the server send those rows in the first place. */
function renderWithRail(overrides: Record<string, unknown> = {}) {
  return renderBoard({ filter: { show_untracked: true }, ...overrides });
}

/* A DataTransfer stand-in: jsdom does not implement it. */
function dt() {
  const store: Record<string, string> = {};
  return {
    setData: (k: string, v: string) => { store[k] = v; },
    getData: (k: string) => store[k] ?? "",
    effectAllowed: "",
  };
}

describe("KanbanBoard", () => {
  test("renders one column per status with ticket cards in place", () => {
    renderBoard();
    expect(screen.getByText("Fix the webhook")).toBeTruthy();
    expect(screen.getByText("Old finished thing")).toBeTruthy();
    for (const label of ["Open", "In Progress", "Waiting", "Done"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
    expect(screen.getByText("T-4F2A")).toBeTruthy();
    expect(screen.getByText("Priority: high")).toBeTruthy();
    expect(screen.getByText("stale")).toBeTruthy();
  });

  // A card standing for a ticket has to show its sessions, because a row is
  // the handle you drag to move that chat — a count could not be.
  test("lists a ticket's sessions on its card", () => {
    renderBoard();
    expect(screen.getByTestId("session-row-sess-a")).toBeTruthy();
    expect(screen.getByTestId("session-row-sess-b")).toBeTruthy();
    expect(screen.getByText("First attempt")).toBeTruthy();
    expect(screen.getByText("2 notes")).toBeTruthy();
    expect(screen.getByText("1 open")).toBeTruthy();
  });

  test("clicking a session row opens that session, not the ticket", async () => {
    const { onOpenSession, onOpen } = renderBoard();
    await fireEvent.click(screen.getByTestId("session-row-sess-a"));
    expect(onOpenSession).toHaveBeenCalledWith("sess-a");
    expect(onOpen).not.toHaveBeenCalled();
  });

  test("clicking a card opens the ticket", async () => {
    const { onOpen } = renderBoard();
    await fireEvent.click(screen.getByTestId("ticket-card-T-4F2A"));
    expect(onOpen).toHaveBeenCalledWith("T-4F2A");
  });

  // The left rail: chats with no ticket, which is where you drag FROM.
  test("shows untracked chats with a per-chat create button", () => {
    renderWithRail();
    expect(screen.getByTestId("untracked-loose-1")).toBeTruthy();
    expect(screen.getByText("Quick question about exports")).toBeTruthy();
    expect(screen.getByTitle("Create a ticket from this chat")).toBeTruthy();
  });

  test("creating from an untracked chat prefills its title", async () => {
    renderWithRail();
    await fireEvent.click(screen.getByTitle("Create a ticket from this chat"));
    const input = screen.getByLabelText("New ticket title") as HTMLInputElement;
    expect(input.value).toBe("Quick question about exports");
    expect(screen.getByText("Create & attach")).toBeTruthy();
  });

  // Dropping an untracked chat on a card attaches it — one PUT, then the
  // board reloads because the server also moved its notes.
  test("dropping an untracked session on a card attaches it", async () => {
    const { onReload } = renderWithRail();
    const src = screen.getByTestId("untracked-loose-1");
    const card = screen.getByTestId("ticket-card-T-4F2A");
    const transfer = dt();
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    await fireEvent.dragOver(card, { dataTransfer: transfer });
    await fireEvent.drop(card, { dataTransfer: transfer });

    const put = calls.find((c) => c.method === "PUT");
    expect(put?.url).toContain("/api/tickets/T-4F2A/sessions/loose-1");
    // The reload happens once the request resolves, so let the microtask
    // queue drain before asserting on it.
    await vi.waitFor(() => expect(onReload).toHaveBeenCalled());
  });

  // Dragging a session already on this ticket back onto it is a no-op, not
  // a redundant write.
  test("dropping a session on the ticket it already belongs to does nothing", async () => {
    renderBoard();
    const row = screen.getByTestId("session-row-sess-a");
    const card = screen.getByTestId("ticket-card-T-4F2A");
    const transfer = dt();
    await fireEvent.dragStart(row, { dataTransfer: transfer });
    await fireEvent.drop(card, { dataTransfer: transfer });
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });

  // A ticket dragged to a column changes status; that is a PATCH on the
  // ticket, and must not be confused with a session drop.
  test("dragging a ticket to another column patches its status", async () => {
    renderBoard();
    const card = screen.getByTestId("ticket-card-T-4F2A");
    const column = screen.getByRole("list", { name: "Waiting" });
    const transfer = dt();
    await fireEvent.dragStart(card, { dataTransfer: transfer });
    await fireEvent.dragOver(column, { dataTransfer: transfer });
    await fireEvent.drop(column, { dataTransfer: transfer });

    const patch = calls.find((c) => c.method === "PATCH");
    expect(patch?.url).toContain("/api/tickets/T-4F2A");
  });

  // Dropping a chat onto a column means "this is work, and it is at this
  // stage" — so it becomes its own ticket there rather than being refused
  // for not having one yet.
  test("dropping a session on a column creates a ticket at that status", async () => {
    const { onReload } = renderWithRail();
    const src = screen.getByTestId("untracked-loose-1");
    const column = screen.getByRole("list", { name: "Waiting" });
    const transfer = dt();
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    await fireEvent.dragOver(column, { dataTransfer: transfer });
    await fireEvent.drop(column, { dataTransfer: transfer });

    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toContain("/api/projects/p1/tickets");
    expect(post?.body).toMatchObject({
      status: "waiting",
      session_id: "loose-1",
      title: "Quick question about exports",
      // Dragging it in is claiming it: an unassigned card would say the
      // opposite of what the gesture meant.
      assignee: "u-me",
    });
    await vi.waitFor(() => expect(onReload).toHaveBeenCalled());
  });

  // The untracked chip is load-bearing: it is what makes the server send
  // that list at all, so it belongs with the other filters rather than
  // being a collapse on the rail itself.
  test("the untracked chip is off by default and asks for the rail", async () => {
    const { onFilter } = renderBoard();
    const chip = screen.getByTestId("chip-untracked");
    expect(chip.getAttribute("aria-pressed")).toBe("false");
    await fireEvent.click(chip);
    expect(onFilter).toHaveBeenCalledWith({ show_untracked: true });
  });

  // The count travels even when the rows do not, so the chip can say what
  // switching it on would fetch.
  test("the untracked chip names the count without drawing the rail", () => {
    renderBoard();
    expect(screen.getByTestId("chip-untracked").textContent).toContain("42");
    expect(screen.queryByTestId("untracked-loose-1")).toBeNull();
  });

  test("switching the chip off gives the rail back", async () => {
    const { onFilter } = renderWithRail();
    expect(screen.getByTestId("untracked-loose-1")).toBeTruthy();
    await fireEvent.click(screen.getByTestId("chip-untracked"));
    expect(onFilter).toHaveBeenCalledWith({ show_untracked: false });
  });

  // One page was sent out of a bigger set, and the rail says so instead of
  // implying it holds them all.
  test("the rail shows page-of-total when the server truncated", () => {
    renderWithRail();
    expect(screen.getByText("1/42")).toBeTruthy();
  });

  // Same for a card: rows are a page, and the remainder is a link inward.
  test("a card links to the rest of its sessions", () => {
    renderBoard({
      board: {
        ...board,
        tickets: [{ ...board.tickets[0], sessions: 7 }],
      },
    });
    expect(screen.getByText("+5 more sessions")).toBeTruthy();
  });

  test("toggling a status chip emits the reduced filter", async () => {
    const { onFilter } = renderBoard();
    const chips = screen.getAllByRole("button", { pressed: true });
    const done = chips.find((c) => c.textContent?.trim() === "Done")!;
    await fireEvent.click(done);
    expect(onFilter).toHaveBeenCalledWith({ statuses: ["open", "in_progress", "waiting"] });
  });

  // Turning the last column off is a legitimate request — no cards at all —
  // and has to be distinguishable from the empty list that means "all".
  test("turning every status off asks for no cards, not all of them", async () => {
    const { onFilter } = renderBoard({ filter: { statuses: ["done"] } });
    const done = screen
      .getAllByRole("button", { pressed: true })
      .find((c) => c.textContent?.trim() === "Done")!;
    await fireEvent.click(done);
    // A sentinel entry, not [] — an empty list is the saved shape for "all
    // statuses", so the two have to be spelled differently.
    const arg = onFilter.mock.calls[0][0] as { statuses: string[] };
    expect(arg.statuses).toHaveLength(1);
    expect(arg.statuses[0]).not.toBe("done");
  });

  test("a status filter with every chip off draws no columns", () => {
    renderBoard({ filter: { statuses: [" none"] } });
    expect(screen.queryByText("Fix the webhook")).toBeNull();
    expect(screen.getByText(/No columns selected/)).toBeTruthy();
  });

  // The assignee filter is part of the request, so the board draws what
  // arrived rather than filtering it again — a second filter here would
  // hide nothing and would mask a request that asked for the wrong person.
  test("picking an assignee emits it for the next request", async () => {
    const { onFilter } = renderBoard();
    const select = screen.getByRole("combobox") as HTMLSelectElement;
    await fireEvent.change(select, { target: { value: "me" } });
    expect(onFilter).toHaveBeenCalledWith({ assignee: "me" });
  });

  // Everyone the project names stays listed even while their cards are
  // filtered out — otherwise the dropdown could not be reset from itself.
  test("the assignee list survives a filter that excluded those people", () => {
    renderBoard({
      filter: { assignee: "u-me" },
      board: { ...board, tickets: [board.tickets[0]] },
    });
    expect(screen.getByRole("option", { name: "Assignee: Other Person" })).toBeTruthy();
  });

  // Statuses are per project, so a renamed board must draw ITS columns —
  // not the built-in four with the new names ignored.
  test("columns come from the project's own statuses", () => {
    renderBoard({
      board: {
        ...board,
        statuses: [
          { key: "triage", label: "Triage" },
          { key: "coding", label: "Coding" },
          { key: "shipped", label: "Shipped", terminal: true },
        ],
        tickets: [{ ...board.tickets[0], status: "triage" }],
      },
      filter: {},
    });
    expect(screen.getAllByText("Triage").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Shipped").length).toBeGreaterThan(0);
    expect(screen.queryByText("In Progress")).toBeNull();
    expect(screen.getByText("Fix the webhook")).toBeTruthy();
  });

  test("a status with no label falls back to its key", () => {
    renderBoard({
      board: {
        ...board,
        statuses: [{ key: "wip" }, { key: "shipped", terminal: true }],
        tickets: [],
      },
    });
    expect(screen.getAllByText("wip").length).toBeGreaterThan(0);
  });

  test("the new-ticket form opens and closes", async () => {
    renderBoard();
    await fireEvent.click(screen.getByText("+ New ticket"));
    expect(screen.getByLabelText("New ticket title")).toBeTruthy();
    await fireEvent.click(screen.getByText("Cancel"));
    expect(screen.queryByLabelText("New ticket title")).toBeNull();
  });
});
