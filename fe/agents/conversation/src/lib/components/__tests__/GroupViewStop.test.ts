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

/* The view joins the shared session stream; the test pushes `agent` frames
   into it and drives its status. A page-level EventSource would be a second
   connection, so constructing one fails the test. */
const { streams } = vi.hoisted(() => ({ streams: [] as FakeStream[] }));
type FakeStream = {
  sid: string;
  cbs: ((e: unknown) => void)[];
  statusCbs: ((s: string) => void)[];
  closed: boolean;
  push(type: string, body: unknown): void;
  setStatus(s: string): void;
};
vi.mock("../../stores/sse.js", () => ({
  connectSession: vi.fn((_base: string, sid: string) => {
    const st: FakeStream = {
      sid, cbs: [], statusCbs: [], closed: false,
      push(type, body) { this.cbs.forEach((cb) => cb({ session_id: sid, type, data: JSON.stringify(body) })); },
      setStatus(s) { this.statusCbs.forEach((cb) => cb(s)); },
    };
    streams.push(st);
    return {
      onEvent: (cb: (e: unknown) => void) => st.cbs.push(cb),
      status: { subscribe: (cb: (s: string) => void) => { st.statusCbs.push(cb); cb("connecting"); return () => {}; } },
      resync: () => {},
      close: () => { st.closed = true; },
    };
  }),
}));
vi.stubGlobal("EventSource", class { constructor() { throw new Error("GroupView must not open its own EventSource"); } });

import GroupView from "../GroupView.svelte";
import { killProcess } from "../../api/processes.js";
import { toastOk } from "@wick-fe/common-stores";
import { groupConversation } from "../../api/team.js";

const member = (id: string, handle: string) => ({ id, handle, name: handle, avatar: {}, disabled: false });
const group = {
  id: "g1", name: "Crew", members: [member("a1", "ana"), member("b1", "bob")],
  default_responder: "captain", responder: "ana", max_hops_override: 0, members_max_hops: 3, max_hops: 3,
  last_active: null, last_preview: "", unread: false,
};

describe("GroupView — composer Stop", () => {
  beforeEach(() => { vi.clearAllMocks(); streams.length = 0; });

  test("kills the backing session of every member typing, no confirm", async () => {
    render(GroupView, { props: { base: "/api", group, agents: [], onSettings: () => {} } as never });
    expect(screen.queryByRole("button", { name: "Stop" })).toBeNull();
    streams[0].push("group_typing", { agent_id: "a1", state: "start", session_id: "s-ana" });
    streams[0].push("group_typing", { agent_id: "b1", state: "start", session_id: "s-bob" });
    await fireEvent.click(await screen.findByRole("button", { name: "Stop" }));
    await waitFor(() => expect(toastOk).toHaveBeenCalledWith("Stopped"));
    expect(killProcess).toHaveBeenCalledWith("/api", "s-ana");
    expect(killProcess).toHaveBeenCalledWith("/api", "s-bob");
    expect(screen.queryByTestId("group-typing")).toBeNull();
  });
});

describe("GroupView — shared stream instead of polling", () => {
  beforeEach(() => { vi.clearAllMocks(); streams.length = 0; });

  test("joins the group's session on the shared stream and renders a pushed turn", async () => {
    const { unmount } = render(GroupView, { props: { base: "/api", group, agents: [], onSettings: () => {} } as never });
    expect(streams).toHaveLength(1);
    expect(streams[0].sid).toBe("g1");
    streams[0].push("group_turn", { turn_id: "t1", role: "assistant", text: "pushed hello", speaker: { agent_id: "a1", handle: "ana" } });
    expect(await screen.findByText("pushed hello")).toBeTruthy();
    unmount();
    expect(streams[0].closed).toBe(true);
  });

  test("no poll while the stream is up, even with a reply pending; a reconnect reloads", async () => {
    vi.useFakeTimers();
    try {
      render(GroupView, { props: { base: "/api", group, agents: [], onSettings: () => {} } as never });
      streams[0].setStatus("connected");
      await vi.advanceTimersByTimeAsync(0);
      const loads = () => vi.mocked(groupConversation).mock.calls.length;
      const afterMount = loads();
      // The old view re-read the thread every 4 s; now nothing does while
      // the stream is up.
      await vi.advanceTimersByTimeAsync(5 * 60_000);
      expect(loads()).toBe(afterMount);
      // Drop then recover: one reload to cover what the gap swallowed.
      streams[0].setStatus("error");
      streams[0].setStatus("connected");
      await vi.advanceTimersByTimeAsync(0);
      expect(loads()).toBe(afterMount + 1);
    } finally {
      vi.useRealTimers();
    }
  });
});
