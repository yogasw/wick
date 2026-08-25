import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import SubAgentModal from "../SubAgentModal.svelte";
import type { SubAgentItem } from "../../types/agents.js";

/* Every api function returns a tagged marker instead of a real Effect;
   runPromise resolves whatever `replies` holds for that tag. That keeps the
   test driving data through the component's real code paths without an
   Effect runtime. */
const { replies, calls } = vi.hoisted(() => ({
  replies: new Map<string, unknown>(),
  calls: { sent: [] as { id: string; text: string }[], stopped: [] as string[] },
}));

function marker(tag: string) {
  const o: Record<string, unknown> = { __tag: tag };
  o.pipe = () => o;
  return o;
}

vi.mock("effect", () => ({
  Effect: {
    runPromise: vi.fn((eff: { __tag?: string }) =>
      Promise.resolve(replies.get(eff?.__tag ?? "") ?? null),
    ),
    provide: vi.fn((eff: unknown) => eff),
    map: vi.fn(() => (eff: unknown) => eff),
  },
}));

vi.mock("@wick-fe/common-api", () => ({ WickClientLayer: {} }));
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}));

vi.mock("../../stores/sse.js", () => ({
  connectSession: () => ({
    close: vi.fn(),
    status: { subscribe: (fn: (v: string) => void) => { fn("connected"); return () => {}; } },
    onEvent: () => {},
  }),
}));

vi.mock("../../api/sessions.js", () => ({
  getConversation: vi.fn((_base: string, id: string) => marker(`conversation:${id}`)),
  getTurnTrace: vi.fn(() => marker("trace")),
}));

vi.mock("../../api/subagents.js", () => ({
  getSubAgents: vi.fn((_base: string, id: string) => marker(`subagents:${id}`)),
  interruptSubAgent: vi.fn((_base: string, delegationId: string) => {
    calls.stopped.push(delegationId);
    return marker("interrupt");
  }),
}));

vi.mock("../../api/messages.js", () => ({
  sendMessage: vi.fn((_base: string, id: string, payload: { text: string }) => {
    calls.sent.push({ id, text: payload.text });
    return marker("send");
  }),
}));

function subAgent(over: Partial<SubAgentItem> = {}): SubAgentItem {
  return {
    delegation_id: "d1",
    child_session_id: "root--sub-9f2c81ab40de",
    profile_key: "test-agent",
    label: "Smoke test the sub-agent system",
    status: "done",
    lifecycle: "",
    depth: 0,
    turns_used: 1,
    max_turns: 3,
    ...over,
  };
}

function turn(text: string, id = "t1") {
  return {
    turn_id: id,
    role: "assistant",
    agent: "claude",
    provider: "claude",
    text,
    timestamp: 0,
    truncated: false,
    interrupted: false,
    has_trace: false,
    events: [],
    attachments: [],
  };
}

const props = () => ({
  base: "/tools/agents",
  sessionId: "root",
  row: subAgent(),
  onClose: vi.fn(),
});

beforeEach(() => {
  replies.clear();
  calls.sent.length = 0;
  calls.stopped.length = 0;
});

describe("SubAgentModal", () => {
  test("renders the selected sub-agent's own transcript", async () => {
    replies.set("conversation:root--sub-9f2c81ab40de", {
      turns: [turn("Saya berjalan sebagai sub-agent.")],
    });

    render(SubAgentModal, { props: props() });

    expect(await screen.findByText(/Saya berjalan sebagai sub-agent/)).toBeTruthy();
    // The delegated task is shown alongside it, so the transcript is not
    // read without knowing what was asked for.
    expect(screen.getByText("Smoke test the sub-agent system")).toBeTruthy();
  });

  test("lists the sub-agent's own sub-agents and drills into one", async () => {
    const child = subAgent({
      delegation_id: "d2",
      child_session_id: "root--sub-9f2c81ab40de--sub-71ee03cc95a1",
      profile_key: "researcher",
      label: "Dig through the changelog",
      depth: 1,
    });
    replies.set("conversation:root--sub-9f2c81ab40de", { turns: [turn("delegating…")] });
    replies.set("subagents:root--sub-9f2c81ab40de", [child]);
    replies.set("conversation:root--sub-9f2c81ab40de--sub-71ee03cc95a1", {
      turns: [turn("changelog says…", "t2")],
    });

    render(SubAgentModal, { props: props() });

    const chip = await screen.findByRole("button", { name: "researcher" });
    await fireEvent.click(chip);

    // The grandchild's transcript replaces the child's, and the breadcrumb
    // keeps both so there is a way back.
    expect(await screen.findByText(/changelog says/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "test-agent" })).toBeTruthy();
  });

  test("Back pops one breadcrumb level, and closes at the root", async () => {
    const child = subAgent({
      delegation_id: "d2",
      child_session_id: "root--sub-9f2c81ab40de--sub-71ee03cc95a1",
      profile_key: "researcher",
      depth: 1,
    });
    replies.set("conversation:root--sub-9f2c81ab40de", { turns: [turn("delegating…")] });
    replies.set("subagents:root--sub-9f2c81ab40de", [child]);
    replies.set("conversation:root--sub-9f2c81ab40de--sub-71ee03cc95a1", {
      turns: [turn("changelog says…", "t2")],
    });

    const p = props();
    render(SubAgentModal, { props: p });

    await fireEvent.click(await screen.findByRole("button", { name: "researcher" }));
    await screen.findByText(/changelog says/);

    const back = screen.getByRole("button", { name: /back to the previous sub-agent/i });
    await fireEvent.click(back);

    // One level up: the child's transcript is back and the modal is still open.
    expect(await screen.findByText(/delegating/)).toBeTruthy();
    expect(p.onClose).not.toHaveBeenCalled();

    await fireEvent.click(screen.getByRole("button", { name: /^close$/i }));
    expect(p.onClose).toHaveBeenCalled();
  });

  test("the composer sends to the sub-agent's own session", async () => {
    replies.set("conversation:root--sub-9f2c81ab40de", { turns: [turn("done")] });
    render(SubAgentModal, { props: props() });

    const box = await screen.findByPlaceholderText(/follow-up/i);
    await fireEvent.input(box, { target: { value: "why did you stop there?" } });
    await fireEvent.keyDown(box, { key: "Enter" });

    await waitFor(() => expect(calls.sent.length).toBe(1));
    expect(calls.sent[0]).toEqual({
      id: "root--sub-9f2c81ab40de",
      text: "why did you stop there?",
    });
  });

  // Stopping is offered only while there is something to stop; a Stop button
  // on a finished sub-agent would be a click that does nothing.
  test("Stop shows only while the sub-agent is live, and interrupts its delegation", async () => {
    replies.set("conversation:root--sub-9f2c81ab40de", { turns: [] });

    const { unmount } = render(SubAgentModal, { props: props() });
    expect(screen.queryByRole("button", { name: /^stop$/i })).toBeNull();
    unmount();

    render(SubAgentModal, {
      props: { ...props(), row: subAgent({ status: "running" }) },
    });
    await fireEvent.click(await screen.findByRole("button", { name: /^stop$/i }));
    expect(calls.stopped).toEqual(["d1"]);
  });
});
