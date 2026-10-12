import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const saveTeamSettings = vi.fn();
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  saveTeamSettings: (...a: unknown[]) => saveTeamSettings(...a),
  // The ticket page's room block: no room, no Team agents.
  listAgentRoster: () => Promise.resolve({ agents: [], captain_id: "" }),
  runApi: <T,>(p: Promise<T>) => p,
}));
const getProjectOptions = vi.fn();
vi.mock("../../../api/options.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/options.js")>()),
  getProjectOptions: (...a: unknown[]) => getProjectOptions(...a),
}));

/* The combined board's requests: one board per picked project, and the
   filter under the Team key. Effects, as the real client returns. */
const getProjectTickets = vi.fn();
const getTicketFilter = vi.fn();
const saveTicketFilter = vi.fn();
const getTicket = vi.fn();
vi.mock("../../../api/tickets.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/tickets.js")>()),
  getTicket: (...a: unknown[]) => getTicket(...a),
  getTicketRoom: () => Promise.resolve({ room: null }),
  getProjectTickets: (...a: unknown[]) => getProjectTickets(...a),
  getTicketFilter: (...a: unknown[]) => getTicketFilter(...a),
  saveTicketFilter: (...a: unknown[]) => saveTicketFilter(...a),
}));

/* The live-update stream: the test holds its handlers and plays signals. */
type StreamHandlers = { onTicket?: (t: { project_id: string; ticket_id: string }) => void; onStatus?: (s: string) => void };
let stream: StreamHandlers = {};
vi.mock("../../../stores/sessionsStream.js", () => ({
  connectSessionsStream: (_b: string, h: StreamHandlers) => {
    stream = h;
    return () => {};
  },
}));

import { Effect } from "effect";
import TicketsPage from "../TicketsPage.svelte";
import type { TicketBoard } from "../../../types/agents.js";

const boardOf = (id: string, title: string): TicketBoard => ({
  config: { enabled: true },
  statuses: [{ key: "open", label: "Open" }, { key: "done", label: "Done", terminal: true }],
  users: {},
  untracked: [],
  tickets: [{ id, title, status: "open", session_rows: [], sessions: 0, notes: 0, open_tasks: 0, updated_at: "2026-10-01T00:00:00Z", created_at: "2026-10-01T00:00:00Z", stale: false }],
});

const options = () => [
  { id: "p2", name: "Beta", ticketEnabled: true },
  { id: "p1", name: "Alpha", ticketEnabled: true },
  { id: "p3", name: "No board", ticketEnabled: false },
];

function mount(selected: string[] | null, route = {}) {
  const onPicked = vi.fn();
  const onRoute = vi.fn();
  const onOpenSession = vi.fn();
  render(TicketsPage, { props: { base: "/tools/agents", selected, route, onPicked, onRoute, onOpenSession } });
  return { onPicked, onRoute, onOpenSession };
}

const detailOf = (id: string) => ({
  ticket: { id, project_id: "p1", title: "Seven", status: "open", created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" },
  config: { enabled: true },
  sessions: [{ id: "s-1", label: "First chat" }],
  notes: [],
  statuses: [{ key: "open", label: "Open" }],
  users: {},
});

describe("TicketsPage", () => {
  beforeEach(() => {
    saveTeamSettings.mockReset();
    getProjectOptions.mockReset();
    getProjectOptions.mockResolvedValue(options());
    getProjectTickets.mockReset();
    getProjectTickets.mockImplementation((_b: string, id: string) =>
      Effect.succeed(boardOf(id === "p1" ? "T-A" : "T-B", id === "p1" ? "Alpha ticket" : "Beta ticket")),
    );
    getTicketFilter.mockReset();
    getTicketFilter.mockReturnValue(Effect.succeed({}));
    saveTicketFilter.mockReset();
    saveTicketFilter.mockReturnValue(Effect.succeed({ status: "ok" }));
    stream = {};
    getTicket.mockReset();
    getTicket.mockImplementation((_b: string, id: string) => Effect.succeed(detailOf(id)));
    vi.unstubAllGlobals();
  });

  const callsFor = (id: string) => getProjectTickets.mock.calls.filter((c) => c[1] === id);

  test("a filter click keeps the board mounted and the project select where it was", async () => {
    mount(["p1", "p2"]);
    await screen.findByTestId("ticket-card-T-A");
    const sel = (await screen.findByTestId("board-project")) as HTMLSelectElement;
    await fireEvent.change(sel, { target: { value: "p2" } });
    expect(screen.queryByTestId("ticket-card-T-A")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await waitFor(() => expect(callsFor("p1")).toHaveLength(2));
    await waitFor(() => expect(callsFor("p2")).toHaveLength(2));
    // Same element (not remounted), same choice, Alpha still hidden.
    expect(screen.getByTestId("board-project")).toBe(sel);
    expect(sel.value).toBe("p2");
    expect(screen.queryByTestId("ticket-card-T-A")).toBeNull();
    expect(screen.getByTestId("ticket-card-T-B")).toBeTruthy();
    expect(screen.queryByTestId("tickets-board-loading-p1")).toBeNull();
  });

  test("a ticket signal reloads that project only, without emptying the board", async () => {
    mount(["p1", "p2"]);
    await screen.findByTestId("ticket-card-T-A");
    stream.onStatus?.("connected");
    stream.onTicket?.({ project_id: "p1", ticket_id: "T-A" });
    stream.onTicket?.({ project_id: "p-other", ticket_id: "T-X" });
    expect(screen.getByTestId("ticket-card-T-A")).toBeTruthy();
    await waitFor(() => expect(callsFor("p1")).toHaveLength(2));
    expect(callsFor("p2")).toHaveLength(1);
    expect(getProjectTickets.mock.calls.some((c) => c[1] === "p-other")).toBe(false);
    expect(screen.getByTestId("ticket-card-T-B")).toBeTruthy();
  });

  test("while the stream is down the poll reloads every picked project", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      mount(["p1", "p2"]);
      await screen.findByTestId("ticket-card-T-A");
      await screen.findByTestId("ticket-card-T-B");
      vi.advanceTimersByTime(60_000);
      vi.useRealTimers(); // waitFor polls on a real interval
      await waitFor(() => expect(callsFor("p1")).toHaveLength(2));
      expect(callsFor("p2")).toHaveLength(2);
      expect(screen.getByTestId("ticket-card-T-A")).toBeTruthy();
      expect(screen.getByTestId("ticket-card-T-B")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  test("a slow older answer never replaces a newer one", async () => {
    let late!: (b: TicketBoard) => void;
    getProjectTickets.mockImplementationOnce(() => Effect.promise(() => new Promise<TicketBoard>((r) => { late = r; })));
    getProjectTickets.mockImplementation((_b: string, id: string) =>
      Effect.succeed(boardOf(id === "p1" ? "T-NEW" : "T-B", "fresh")),
    );
    mount(["p1", "p2"]);
    await waitFor(() => expect(late).toBeTypeOf("function"));
    stream.onTicket?.({ project_id: "p1", ticket_id: "T-NEW" });
    expect(await screen.findByTestId("ticket-card-T-NEW", {}, { timeout: 2000 })).toBeTruthy();
    late(boardOf("T-OLD", "stale"));
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByTestId("ticket-card-T-OLD")).toBeNull();
  });

  test("Retry on a failed project reloads that project only", async () => {
    getProjectTickets.mockImplementation((_b: string, id: string) =>
      id === "p2" ? Effect.fail(new Error("forbidden")) : Effect.succeed(boardOf("T-A", "Alpha ticket")),
    );
    mount(["p1", "p2"]);
    const row = await screen.findByTestId("tickets-board-error-p2");
    await fireEvent.click(row.querySelector("button")!);
    await waitFor(() => expect(callsFor("p2")).toHaveLength(2));
    expect(callsFor("p1")).toHaveLength(1);
  });

  test("load more pages one project's rail from its own cursor", async () => {
    getTicketFilter.mockReturnValue(Effect.succeed({ show_untracked: true }));
    getProjectTickets.mockImplementation((_b: string, id: string) =>
      Effect.succeed({
        ...boardOf(id === "p1" ? "T-A" : "T-B", "t"),
        untracked: [{ id: "loose-" + id, label: "Loose " + id }],
        untracked_total: 9,
        untracked_next: "cur-" + id,
      }),
    );
    mount(["p1", "p2"]);
    await fireEvent.click(await screen.findByTestId("rail-more-p1"));
    await waitFor(() => expect(callsFor("p1")).toHaveLength(2));
    expect(callsFor("p1")[1][2]).toMatchObject({ untrackedAfter: "cur-p1", untracked: true, statuses: [] });
    expect(callsFor("p2")).toHaveLength(1);
  });

  test("nothing picked: only projects with ticket mode on, Done waits for a tick", async () => {
    mount([]);
    expect(await screen.findByText("Select projects first")).toBeTruthy();
    expect(await screen.findByLabelText(/Alpha/)).toBeTruthy();
    expect(screen.getByLabelText(/Beta/)).toBeTruthy();
    expect(screen.queryByLabelText(/No board/)).toBeNull();
    expect((screen.getByRole("button", { name: "Done" }) as HTMLButtonElement).disabled).toBe(true);
  });

  test("Done saves ticket_projects with the ticked ids, in the order picked", async () => {
    saveTeamSettings.mockImplementation((_b: string, body: Record<string, unknown>) => Promise.resolve({ prompt: "", open_team: false, idle_animations: true, max_prompt_bytes: 1, ...body }));
    const { onPicked } = mount([]);
    await fireEvent.click(await screen.findByLabelText(/Beta/));
    await fireEvent.click(screen.getByLabelText(/Alpha/));
    await fireEvent.click(screen.getByRole("button", { name: "Done · 2 selected" }));
    await waitFor(() => expect(saveTeamSettings).toHaveBeenCalledWith("/tools/agents", { ticket_projects: ["p2", "p1"] }));
    await waitFor(() => expect(onPicked).toHaveBeenCalledWith(expect.objectContaining({ ticket_projects: ["p2", "p1"] })));
  });

  test("a refused save stays on the picker with the reason", async () => {
    saveTeamSettings.mockRejectedValue(new Error("ticket_projects: no such project"));
    const { onPicked } = mount([]);
    await fireEvent.click(await screen.findByLabelText(/Alpha/));
    await fireEvent.click(screen.getByRole("button", { name: "Done · 1 selected" }));
    expect(await screen.findByText("ticket_projects: no such project")).toBeTruthy();
    expect(onPicked).not.toHaveBeenCalled();
  });

  test("with a selection: every picked board on one board, no picker", async () => {
    mount(["p1", "p2"]);
    expect(await screen.findByTestId("ticket-card-T-A")).toBeTruthy();
    expect(await screen.findByTestId("ticket-card-T-B")).toBeTruthy();
    expect(getProjectTickets.mock.calls.map((c) => c[1]).sort()).toEqual(["p1", "p2"]);
    expect(screen.queryByTestId("tickets-picker")).toBeNull();
    // The combined filter is read under its own key, never a project's.
    expect(getTicketFilter).toHaveBeenCalledWith("/tools/agents", "__team__");
  });

  test("one project failing shows an error row; the others still draw", async () => {
    getProjectTickets.mockImplementation((_b: string, id: string) =>
      id === "p2" ? Effect.fail(new Error("forbidden")) : Effect.succeed(boardOf("T-A", "Alpha ticket")),
    );
    mount(["p1", "p2"]);
    const err = await screen.findByTestId("tickets-board-error-p2");
    expect(err.textContent).toContain("forbidden");
    expect(await screen.findByTestId("ticket-card-T-A")).toBeTruthy();
    expect(screen.queryByTestId("tickets-board-error-p1")).toBeNull();
  });

  test("a card opens its ticket page under /team/t/<project>/<ticket>", async () => {
    const { onRoute } = mount(["p1", "p2"]);
    await fireEvent.click(await screen.findByTestId("ticket-card-T-B"));
    expect(onRoute).toHaveBeenCalledWith({ project: "p2", ticket: "T-B" });
  });

  test("a filter change is saved under the Team key", async () => {
    mount(["p1", "p2"]);
    await screen.findByTestId("ticket-card-T-A");
    await fireEvent.click(screen.getByTestId("chip-untracked"));
    await waitFor(() => expect(saveTicketFilter).toHaveBeenCalled(), { timeout: 2000 });
    expect(saveTicketFilter.mock.calls.every((c) => c[1] === "__team__")).toBe(true);
    expect(saveTicketFilter.mock.calls.at(-1)![2]).toEqual({ show_untracked: true });
  });

  test("a ticket route renders TicketDetail with ← Tickets and its 📁 project", async () => {
    const { onRoute } = mount(["p1"], { project: "p1", ticket: "T-7" });
    const page = await screen.findByTestId("tickets-ticket");
    expect(page.dataset.ticket).toBe("T-7");
    expect(await screen.findByText("Seven")).toBeTruthy();
    expect(getTicket.mock.calls[0][1]).toBe("T-7");
    // The route's project travels with the ticket id (ids are per project).
    expect(getTicket.mock.calls[0][2]).toBe("p1");
    expect(screen.getByTestId("ticket-detail-project").textContent).toMatch(/^📁 \S/);
    await fireEvent.click(screen.getByRole("button", { name: "Tickets" }));
    expect(onRoute).toHaveBeenCalledWith({});
  });

  test("the ticket page holds the room block next to its sessions", async () => {
    mount(["p1"], { project: "p1", ticket: "T-7" });
    expect((await screen.findByTestId("room-empty")).textContent).toContain("No room yet");
    expect(screen.getByText("First chat")).toBeTruthy();
  });

  test("a chat row on the ticket page goes to onOpenSession with its project", async () => {
    const { onOpenSession } = mount(["p1"], { project: "p1", ticket: "T-7" });
    await fireEvent.click(await screen.findByText("First chat"));
    expect(onOpenSession).toHaveBeenCalledWith("s-1", "p1");
  });

  test("a ticket that cannot be opened says so and keeps the way back", async () => {
    getTicket.mockReturnValue(Effect.fail(new Error("project not accessible")));
    const { onRoute } = mount([], { project: "p9", ticket: "T-X" });
    const err = await screen.findByTestId("ticket-detail-error");
    expect(err.textContent).toContain("Couldn't open this ticket");
    expect(err.textContent).toContain("project not accessible");
    await fireEvent.click(screen.getByRole("button", { name: "Tickets" }));
    expect(onRoute).toHaveBeenCalledWith({});
  });

  test("a move that empties a ticket offers its deletion, as on a project board", async () => {
    getTicketFilter.mockReturnValue(Effect.succeed({ show_untracked: true }));
    getProjectTickets.mockImplementation((_b: string, id: string) =>
      Effect.succeed({ ...boardOf(id === "p1" ? "T-A" : "T-B", "t"), untracked: [{ id: "loose-" + id, label: "Loose " + id }] }),
    );
    const fetched: { method: string; url: string }[] = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(typeof input === "string" ? input : ((input as Request).url ?? input));
      fetched.push({ method: (init?.method ?? "GET").toUpperCase(), url });
      const answer = url.includes("/sessions/loose-p1") ? { status: "ok", emptied_ticket: { id: "T-OLD", title: "Old husk" } } : {};
      return new Response(JSON.stringify(answer), { status: 200, headers: { "Content-Type": "application/json" } });
    }));
    mount(["p1", "p2"]);
    const src = await screen.findByTestId("untracked-loose-p1");
    const target = screen.getByTestId("ticket-card-T-A");
    const store: Record<string, string> = {};
    const transfer = { setData: (k: string, v: string) => { store[k] = v; }, getData: (k: string) => store[k] ?? "", effectAllowed: "", dropEffect: "" };
    await fireEvent.dragStart(src, { dataTransfer: transfer });
    await fireEvent.dragOver(target, { dataTransfer: transfer });
    await fireEvent.drop(target, { dataTransfer: transfer });
    expect(await screen.findByText("Delete the empty ticket?")).toBeTruthy();
    const before = callsFor("p1").length;
    await fireEvent.click(screen.getByRole("button", { name: "Delete ticket" }));
    await waitFor(() => expect(fetched.some((c) => c.method === "DELETE" && c.url.includes("/api/tickets/T-OLD"))).toBe(true));
    await waitFor(() => expect(callsFor("p1").length).toBeGreaterThan(before));
  });

  test("settings failed to load: the reason and Retry, never the picker", async () => {
    const onRetry = vi.fn();
    render(TicketsPage, { props: { base: "/tools/agents", selected: null, route: {}, loadError: "boom", onRetry, onPicked: vi.fn(), onRoute: vi.fn() } });
    expect((await screen.findByTestId("tickets-load-error")).textContent).toContain("boom");
    expect(screen.queryByTestId("tickets-picker")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalled();
  });

  test("a ticket link with nothing picked opens the ticket, not the picker", async () => {
    mount([], { project: "p1", ticket: "T-7" });
    expect((await screen.findByTestId("tickets-ticket")).dataset.ticket).toBe("T-7");
    expect(screen.queryByTestId("tickets-picker")).toBeNull();
  });
});
