import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

vi.mock("@wick-fe/common-stores", () => ({ toastOk: vi.fn(), toastError: vi.fn(), toastWarn: vi.fn() }));

const kill = (sid: string) => ({ kill: sid });
vi.mock("../../api/processes.js", () => ({ killProcess: vi.fn((_b: string, sid: string) => kill(sid)) }));

vi.mock("../../api/team.js", () => ({
  groupConversation: vi.fn(() => ({ load: true })),
  sendToGroup: vi.fn(() => ({})),
  markGroupRead: vi.fn(() => ({})),
  runApi: vi.fn(async (e: { load?: boolean }) => (e?.load ? { turns: [] } : {})),
}));

/* One EventSource per view; the test fires its pushes by name. */
const { sources } = vi.hoisted(() => ({ sources: [] as FakeES[] }));
class FakeES {
  handlers: Record<string, (ev: MessageEvent) => void> = {};
  constructor() { sources.push(this); }
  addEventListener(name: string, fn: (ev: MessageEvent) => void) { this.handlers[name] = fn; }
  close() {}
  emit(name: string, data: unknown) { this.handlers[name]?.({ data: JSON.stringify(data) } as MessageEvent); }
}
vi.stubGlobal("EventSource", FakeES);

import GroupView from "../GroupView.svelte";
import { killProcess } from "../../api/processes.js";
import { toastOk } from "@wick-fe/common-stores";

const member = (id: string, handle: string) => ({ id, handle, name: handle, avatar: {}, disabled: false });
const group = {
  id: "g1", name: "Crew", members: [member("a1", "ana"), member("b1", "bob")],
  default_responder: "captain", responder: "ana", max_hops_override: 0, members_max_hops: 3, max_hops: 3,
  last_active: null, last_preview: "", unread: false,
};

describe("GroupView — composer Stop", () => {
  beforeEach(() => { vi.clearAllMocks(); sources.length = 0; });

  test("kills the backing session of every member typing, no confirm", async () => {
    render(GroupView, { props: { base: "/api", group, agents: [], onSettings: () => {} } as never });
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    sources[0].emit("group_typing", { agent_id: "a1", state: "start", session_id: "s-ana" });
    sources[0].emit("group_typing", { agent_id: "b1", state: "start", session_id: "s-bob" });
    await fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(toastOk).toHaveBeenCalledWith("Stopped"));
    expect(killProcess).toHaveBeenCalledWith("/api", "s-ana");
    expect(killProcess).toHaveBeenCalledWith("/api", "s-bob");
    expect(screen.queryByTestId("group-typing")).toBeNull();
  });
});
