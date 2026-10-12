import { describe, test, expect, vi, beforeEach } from "vitest";

const saveTeamSettings = vi.fn();
const listAgentRoster = vi.fn();
vi.mock("./api/team.js", async (orig) => ({
  ...(await orig<typeof import("./api/team.js")>()),
  saveTeamSettings: (...a: unknown[]) => saveTeamSettings(...a),
  listAgentRoster: (...a: unknown[]) => listAgentRoster(...a),
  runApi: <T,>(p: Promise<T>) => p,
}));

import {
  addTicketProject,
  hasTeam,
  resetHasTeam,
  sessionTarget,
  teamTicketsHref,
  ticketJumpFor,
} from "./teamTicketLinks.js";
import type { AgentItem } from "./api/team.js";

const agent = (id: string, handle: string, main: string) => ({ id, handle, main_session_id: main }) as AgentItem;

describe("teamTicketLinks", () => {
  beforeEach(() => {
    saveTeamSettings.mockReset();
    listAgentRoster.mockReset();
    resetHasTeam();
  });

  test("teamTicketsHref: the board and a ticket's page", () => {
    expect(teamTicketsHref("/tools/agents")).toBe("/tools/agents/team/t");
    expect(teamTicketsHref("/tools/agents", "p 1", "T-7")).toBe("/tools/agents/team/t/p%201/T-7");
  });

  test("addTicketProject adds a missing project and saves the list", async () => {
    saveTeamSettings.mockResolvedValue({ ticket_projects: ["p1", "p2"] });
    expect(await addTicketProject("/b", ["p1"], "p2")).toEqual(["p1", "p2"]);
    expect(saveTeamSettings).toHaveBeenCalledWith("/b", { ticket_projects: ["p1", "p2"] });
  });

  test("addTicketProject writes nothing when the project is there or the selection is unknown", async () => {
    expect(await addTicketProject("/b", ["p1", "p2"], "p2")).toBeNull();
    expect(await addTicketProject("/b", null, "p2")).toBeNull();
    expect(saveTeamSettings).not.toHaveBeenCalled();
  });

  test("sessionTarget: a chat with a roster agent opens in its Team chat", async () => {
    const agents = [agent("a1", "helper", "main-1")];
    expect(await sessionTarget("/b", "side-1", agents, async () => "a1")).toEqual({ kind: "team", handle: "helper", session: "side-1" });
    expect(await sessionTarget("/b", "main-1", agents, async () => "a1")).toEqual({ kind: "team", handle: "helper", session: null });
  });

  test("sessionTarget: any other chat, or an unreadable meta, opens in Agents › Projects", async () => {
    const agents = [agent("a1", "helper", "main-1")];
    const projects = { kind: "projects", href: "/b/sessions/s-9" };
    expect(await sessionTarget("/b", "s-9", agents, async () => undefined)).toEqual(projects);
    expect(await sessionTarget("/b", "s-9", agents, async () => "someone-else")).toEqual(projects);
    expect(await sessionTarget("/b", "s-9", agents, async () => { throw new Error("404"); })).toEqual(projects);
  });

  test("ticketJumpFor in Team opens Team › Tickets through the host", () => {
    const onOpenTicket = vi.fn();
    const go = vi.fn();
    const j = ticketJumpFor("/b", "p1", "T-7", { onOpenTicket }, go);
    expect(j.label).toBe("Open in Team › Tickets ↗");
    j.onJump();
    expect(onOpenTicket).toHaveBeenCalledWith("p1", "T-7");
    expect(go).not.toHaveBeenCalled();
  });

  test("ticketJumpFor in Team without a host opener goes to /team/t/… by URL", () => {
    const go = vi.fn();
    ticketJumpFor("/b", "p1", "T-7", {}, go).onJump();
    expect(go).toHaveBeenCalledWith("/b/team/t/p1/T-7");
    ticketJumpFor("/b", "p1", undefined, {}, go).onJump();
    expect(go).toHaveBeenLastCalledWith("/b/team/t");
  });

  test("ticketJumpFor outside Team keeps the project board in Agents › Projects", () => {
    const go = vi.fn();
    const j = ticketJumpFor("/b", "p1", "T-7", undefined, go);
    expect(j.label).toBe("Ticket T-7");
    j.onJump();
    expect(go).toHaveBeenCalledWith("/b/sessions?project=p1&ticket=T-7");
    const board = ticketJumpFor("/b", "p1", undefined, undefined, go);
    expect(board.label).toBe("Ticket board");
    board.onJump();
    expect(go).toHaveBeenLastCalledWith("/b/sessions?project=p1");
  });

  test("hasTeam: a Captain means a Team; none or a failed read means none", async () => {
    listAgentRoster.mockResolvedValueOnce({ agents: [], captain_id: "c1" });
    expect(await hasTeam("/b")).toBe(true);
    resetHasTeam();
    listAgentRoster.mockResolvedValueOnce({ agents: [], captain_id: "" });
    expect(await hasTeam("/b")).toBe(false);
    resetHasTeam();
    listAgentRoster.mockRejectedValueOnce(new Error("down"));
    expect(await hasTeam("/b")).toBe(false);
  });
});

describe("ticketsEntryShown", () => {
  test("only a loaded roster with a Captain shows Tickets", async () => {
    const { ticketsEntryShown } = await import("./teamTicketLinks.js");
    expect(ticketsEntryShown({ loaded: true, hasCaptain: true })).toBe(true);
    expect(ticketsEntryShown({ loaded: true, hasCaptain: false })).toBe(false);
    expect(ticketsEntryShown({ loaded: false, hasCaptain: false })).toBe(false);
    expect(ticketsEntryShown({ loaded: true, loadError: "boom", hasCaptain: true })).toBe(false);
  });
});
