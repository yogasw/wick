import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, waitFor } from "@testing-library/svelte";
import SubAgentModal from "../SubAgentModal.svelte";
import type { SubAgentItem } from "../../types/agents.js";
import { getTurnBlob } from "../../api/sessions.js";

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
  getTurnEvent: vi.fn(() => marker("event")),
  getTurnBlob: vi.fn(async () => new Blob(["png"], { type: "image/png" })),
}));

vi.mock("../ConversationThread.svelte", async () => ({ default: (await import("./stubs/PropsSpy.svelte")).default }));

vi.mock("../../api/subagents.js", () => ({
  getSubAgents: vi.fn((_base: string, id: string) => marker(`subagents:${id}`)),
  interruptSubAgent: vi.fn((_base: string, delegationId: string) => {
    calls.stopped.push(delegationId);
    return marker("interrupt");
  }),
}));

// The modal's caption asks the child session for its own context
// reading. Stubbed to nothing here so these tests stay about the
// transcript — SubAgentModal.meter.test.ts is what covers the caption.
vi.mock("../../api/context.js", () => ({
  fetchSessionContext: vi.fn(() => Promise.reject(new Error("context: 404"))),
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
    label: "Read a screenshot",
    status: "done",
    lifecycle: "",
    depth: 0,
    turns_used: 1,
    max_turns: 3,
    ...over,
  };
}

beforeEach(() => {
  replies.clear();
  delete (globalThis as unknown as { __propsSpy?: unknown }).__propsSpy;
});

describe("SubAgentModal trace binaries", () => {
  /* The bug: a Read of a screenshot in a sub-agent trace showed an image
     chip that could not be clicked. The child's trace had stored the blob,
     but the modal never handed the thread a way to fetch it, so the chip
     read "Not stored in the trace". */
  test("hands the thread a blob loader scoped to the child session", async () => {
    replies.set("conversation:root--sub-9f2c81ab40de", {
      turns: [{ turn_id: "turn1", role: "assistant", agent: "claude", provider: "claude", text: "done", timestamp: 0, truncated: false, interrupted: false, has_trace: true, events: [], attachments: [] }],
    });
    render(SubAgentModal, { props: { base: "/tools/agents", sessionId: "root", row: subAgent(), onClose: vi.fn() } });
    const spy = await waitFor(() => {
      const p = (globalThis as unknown as { __propsSpy?: Record<string, unknown> }).__propsSpy;
      if (!p) throw new Error("thread not rendered");
      return p;
    });
    expect(typeof spy.loadTraceBlob).toBe("function");
    const blob = await (spy.loadTraceBlob as (t: string, r: string) => Promise<Blob>)("turn1", "e15");
    expect(blob.type).toBe("image/png");
    expect(getTurnBlob).toHaveBeenCalledWith("/tools/agents", "root--sub-9f2c81ab40de", "turn1", "e15");
  });
});
