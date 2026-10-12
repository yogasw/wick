import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

vi.mock("@wick-fe/common-stores", async (orig) => ({
  ...(await orig<typeof import("@wick-fe/common-stores")>()),
  toastError: vi.fn(),
  toastOk: vi.fn(),
}));

/* Every API call returns a tagged object; runApi resolves it from `api`. */
const { api, calls, call } = vi.hoisted(() => ({
  api: {} as Record<string, (...a: unknown[]) => unknown>,
  calls: [] as { fn: string; args: unknown[] }[],
  call: (fn: string) => (...args: unknown[]) => ({ fn, args }),
}));
vi.mock("../../../api/tickets.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/tickets.js")>()),
  getTicketRoom: call("getTicketRoom"),
  openTicketRoom: call("openTicketRoom"),
  addRoomMembers: call("addRoomMembers"),
  removeRoomMember: call("removeRoomMember"),
  setRoomShareFull: call("setRoomShareFull"),
}));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  listAgentRoster: call("listAgentRoster"),
  groupConversation: call("groupConversation"),
  sendToGroup: call("sendToGroup"),
  runApi: async (e: { fn: string; args: unknown[] }) => {
    calls.push(e);
    return api[e.fn](...e.args);
  },
}));
const { streams } = vi.hoisted(() => ({ streams: [] as { push(type: string, body: unknown): void }[] }));
vi.mock("../../../stores/sse.js", () => ({
  connectSession: vi.fn((_b: string, sid: string) => {
    const cbs: ((e: unknown) => void)[] = [];
    streams.push({ push: (type, body) => cbs.forEach((cb) => cb({ session_id: sid, type, data: JSON.stringify(body) })) });
    return { onEvent: (cb: (e: unknown) => void) => cbs.push(cb), status: { subscribe: () => () => {} }, close: () => {} };
  }),
}));

import TicketRoom from "../TicketRoom.svelte";
import type { TicketRoom as Room } from "../../../api/tickets.js";

const roomOf = (over: Partial<Room> = {}): Room => ({
  session_id: "room-1",
  ticket_id: "T-1",
  project_id: "p1",
  share_full: false,
  responder: "project",
  members: [
    { kind: "project_default", handle: "project", name: "Billing", home: true, mine: true },
    { kind: "agent", agent_id: "a-log", handle: "log-hunter", name: "Log Hunter", owner_id: "alice", owner_handle: "alice", mine: true },
    { kind: "agent", agent_id: "h-cap", handle: "captain", name: "Captain", owner_id: "hana", owner_handle: "hana", is_captain: true, mine: false },
  ],
  ...over,
});
const roster = {
  agents: [
    { id: "a-cap", handle: "captain", name: "Captain", is_captain: true, disabled: false, avatar: {} },
    { id: "a-log", handle: "log-hunter", name: "Log Hunter", is_captain: false, disabled: false, avatar: {} },
    { id: "s-1", handle: "shared", name: "Shared one", is_captain: false, disabled: false, avatar: {}, role: "viewer" },
  ],
  captain_id: "a-cap",
};
const names = () => calls.map((c) => c.fn);

function mount() {
  return render(TicketRoom, { props: { base: "/b", ticketId: "T-1", projectId: "p1", projectName: "Billing" } });
}

describe("TicketRoom", () => {
  beforeEach(() => {
    calls.length = 0;
    streams.length = 0;
    api.listAgentRoster = () => roster;
    api.groupConversation = () => ({ turns: [] });
    api.sendToGroup = () => ({ status: "sent" });
    api.getTicketRoom = () => ({ room: roomOf() });
    api.openTicketRoom = () => roomOf();
    api.addRoomMembers = (_b, _t, _p, ids) => roomOf({ members: [...roomOf().members, { kind: "agent", agent_id: (ids as string[])[0], handle: "captain", name: "Captain", owner_id: "alice", owner_handle: "alice", mine: true }] });
    api.removeRoomMember = () => roomOf({ members: roomOf().members.filter((m) => m.agent_id !== "a-log") });
    api.setRoomShareFull = (_b, _t, _p, on) => roomOf({ share_full: on as boolean });
  });

  test("no room yet: the empty state and Open room, which creates it", async () => {
    api.getTicketRoom = () => ({ room: null });
    mount();
    expect((await screen.findByTestId("room-empty")).textContent).toContain("No room yet");
    await fireEvent.click(screen.getByRole("button", { name: "Open room" }));
    await screen.findByTestId("room-members");
    expect(names()).toContain("openTicketRoom");
  });

  test("the first message opens the room, then is sent to it", async () => {
    api.getTicketRoom = () => ({ room: null });
    mount();
    await screen.findByTestId("room-empty");
    const box = screen.getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(box, { target: { value: "please look" } });
    await fireEvent.keyDown(box, { key: "Enter" });
    await waitFor(() => expect(names()).toContain("sendToGroup"));
    expect(names().indexOf("openTicketRoom")).toBeLessThan(names().indexOf("sendToGroup"));
    expect(calls.find((c) => c.fn === "sendToGroup")!.args).toEqual(["/b", "room-1", "please look"]);
  });

  test("members: yours removable, the home member not, others' greyed with their owner", async () => {
    mount();
    const mine = await screen.findByTestId("room-member-a-log");
    expect(screen.queryByRole("button", { name: "Remove @project" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove @captain" })).toBeNull();
    expect(screen.getByTestId("room-member-h-cap").getAttribute("title")).toBe("only @hana can ask it");
    expect(screen.getByTestId("room-member-h-cap").textContent).toContain("Captain · @hana");
    expect(mine).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Remove @log-hunter" }));
    await waitFor(() => expect(names()).toContain("removeRoomMember"));
    expect(calls.find((c) => c.fn === "removeRoomMember")!.args).toEqual(["/b", "T-1", "p1", "a-log"]);
  });

  test("+ Add agents lists your own Team only and adds the picked ones in one call", async () => {
    mount();
    await fireEvent.click(await screen.findByTestId("room-add"));
    const modal = screen.getByTestId("room-add-modal");
    expect(modal.textContent).not.toContain("Shared one");
    expect((screen.getByLabelText("Log Hunter") as HTMLInputElement).disabled).toBe(true); // already in
    await fireEvent.click(screen.getByLabelText("Captain"));
    await fireEvent.click(screen.getByRole("button", { name: "Add 1" }));
    await waitFor(() => expect(names()).toContain("addRoomMembers"));
    expect(calls.find((c) => c.fn === "addRoomMembers")!.args).toEqual(["/b", "T-1", "p1", ["a-cap"]]);
  });

  test("🧭 Ask Captain brings your Captain in", async () => {
    mount();
    await fireEvent.click(await screen.findByTestId("room-ask-captain"));
    await waitFor(() => expect(names()).toContain("addRoomMembers"));
    expect(calls.find((c) => c.fn === "addRoomMembers")!.args[3]).toEqual(["a-cap"]);
    await waitFor(() => expect(screen.queryByTestId("room-ask-captain")).toBeNull());
  });

  test("Context: the share toggle sets your own entry", async () => {
    mount();
    const box = (await screen.findByTestId("room-share")) as HTMLInputElement;
    expect(screen.getByTestId("room-context").textContent).toContain("Other people's turns stay on screen only");
    await fireEvent.click(box);
    await waitFor(() => expect(names()).toContain("setRoomShareFull"));
    expect(calls.find((c) => c.fn === "setRoomShareFull")!.args).toEqual(["/b", "T-1", "p1", true]);
    await waitFor(() => expect(screen.getByTestId("room-context").textContent).toContain("other people's included"));
  });

  test("the caption names where a message with no @ goes", async () => {
    mount();
    await screen.findByTestId("room-members");
    expect(screen.getByTestId("room-composer").textContent).toContain("No @ → @project");
  });

  test("a refused mention is a system line, not an agent bubble; pushes arrive on the room's stream", async () => {
    api.groupConversation = () => ({
      turns: [
        { turn_id: "1", ts: "2026-10-12T00:00:00Z", role: "system", kind: "mention_refused", text: "@hana/captain only takes instructions from @hana" },
      ],
    });
    mount();
    const chip = await screen.findByTestId("system-event");
    expect(chip.textContent).toContain("only takes instructions from @hana");
    streams[0].push("system_event", { turn_id: "2", ts: "2026-10-12T00:00:01Z", role: "system", kind: "hop_limit", text: "Stopped after 2 agent-to-agent turns" });
    await waitFor(() => expect(screen.getAllByTestId("system-event")).toHaveLength(2));
  });

  test("every room call names the project; two people's Captains are told apart in the thread", async () => {
    api.groupConversation = () => ({
      turns: [
        { turn_id: "1", ts: "2026-10-12T00:00:00Z", role: "assistant", text: "hana's answer", speaker: { agent_id: "h-cap", handle: "captain", via: "group", owner_id: "hana" } },
      ],
    });
    mount();
    await screen.findByText("hana's answer");
    expect(calls.find((c) => c.fn === "getTicketRoom")!.args).toEqual(["/b", "T-1", "p1"]);
    expect(screen.getByTestId("room-thread").textContent).toContain("Captain · @hana");
  });

});
