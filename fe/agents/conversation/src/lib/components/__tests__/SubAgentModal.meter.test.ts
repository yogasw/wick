import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import SubAgentModal from "../SubAgentModal.svelte";
import type { SubAgentItem } from "../../types/agents.js";
import type { SessionContext } from "../../api/context.js";

/* The read-only caption: which provider/model a sub-agent runs on, how
   full its window is, and what it has spent.

   Read-only is the requirement, not an implementation detail. A
   sub-agent's provider is fixed at spawn from its role, so a control
   here would offer a choice that does not exist — the last test in this
   file is what keeps a picker from being added later. */

const { replies, contextReplies } = vi.hoisted(() => ({
  replies: new Map<string, unknown>(),
  contextReplies: new Map<string, unknown>(),
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
}));

vi.mock("../../api/subagents.js", () => ({
  getSubAgents: vi.fn((_base: string, id: string) => marker(`subagents:${id}`)),
  interruptSubAgent: vi.fn(() => marker("interrupt")),
}));

vi.mock("../../api/messages.js", () => ({ sendMessage: vi.fn(() => marker("send")) }));

vi.mock("../../api/context.js", () => ({
  fetchSessionContext: vi.fn((_base: string, id: string) => {
    const r = contextReplies.get(id);
    return r === undefined ? Promise.reject(new Error("context: 404")) : Promise.resolve(r);
  }),
}));

const SID = "root--sub-9f2c81ab40de";

function subAgent(over: Partial<SubAgentItem> = {}): SubAgentItem {
  return {
    delegation_id: "d1",
    child_session_id: SID,
    profile_key: "test-agent",
    label: "Smoke test the sub-agent system",
    status: "done",
    lifecycle: "",
    depth: 0,
    turns_used: 4,
    max_turns: 40,
    ...over,
  };
}

function context(over: Partial<SessionContext> = {}): SessionContext {
  return {
    session_id: SID,
    used: 0,
    window: 0,
    pct: 0,
    turns: 0,
    totals: {
      input: 0,
      cache_read: 0,
      cache_write: 0,
      output: 0,
      total: 0,
      cost_usd: 0,
      cache_hit_pct: 0,
    },
    providers: [],
    ...over,
  };
}

const props = (row = subAgent()) => ({
  base: "/tools/agents",
  sessionId: "root",
  row,
  onClose: vi.fn(),
});

async function meter() {
  return await screen.findByTestId("subagent-meter");
}

beforeEach(() => {
  replies.clear();
  contextReplies.clear();
  replies.set(`conversation:${SID}`, { turns: [] });
});

describe("SubAgentModal — provider, window and spend", () => {
  test("names the provider and model the sub-agent actually ran on", async () => {
    contextReplies.set(SID, context({ provider: "claude/enginer", model: "claude-opus-5" }));

    render(SubAgentModal, { props: props() });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("claude/enginer");
    });
    expect((await meter()).textContent).toContain("claude-opus-5");
  });

  test("a known window is shown as a percentage with a bar", async () => {
    contextReplies.set(
      SID,
      context({ provider: "claude/enginer", used: 163558, window: 1_000_000, pct: 16.3558 }),
    );

    render(SubAgentModal, { props: props() });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("164k / 1.00M · 16%");
    });
    expect(screen.getByTestId("subagent-context-bar")).toBeTruthy();
  });

  /* The case the server warns about: window 0 means the CLI never
     reported a limit, so there is no denominator to divide by. Tokens,
     no ring, and no invented percentage. */
  test("no window size is shown as tokens with no bar and no percentage", async () => {
    contextReplies.set(SID, context({ provider: "claude/enginer", used: 197787, window: 0 }));

    render(SubAgentModal, { props: props() });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("198k in context");
    });
    expect(screen.queryByTestId("subagent-context-bar")).toBeNull();
  });

  /* A sub-agent that has not run yet has no reading. 0% would say the
     window is empty, which is a different (and wrong) claim. */
  test("a sub-agent with no reading says so instead of showing 0%", async () => {
    contextReplies.set(SID, context());

    render(SubAgentModal, { props: props() });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("no reading yet");
    });
    const text = (await meter()).textContent ?? "";
    expect(text).not.toContain("0%");
    expect(screen.getByTestId("subagent-provider-unknown")).toBeTruthy();
    expect(screen.queryByTestId("subagent-context-bar")).toBeNull();
  });

  test("spend is shown against the delegation's own caps", async () => {
    contextReplies.set(SID, context({ provider: "claude/enginer" }));

    render(SubAgentModal, {
      props: props(
        subAgent({
          tokens_used: 5_248_411,
          max_tokens: 10_000_000,
          input_tokens: 86,
          output_tokens: 45_502,
        }),
      ),
    });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("5.25M / 10.0M tokens");
    });
    const text = (await meter()).textContent ?? "";
    expect(text).toContain("46k out");
    expect(text).toContain("4/40 turns");
  });

  /* 0 means the provider never reported usage. "0 tokens" would read as
     a run that cost nothing. */
  test("a run whose provider reported no usage says not reported", async () => {
    contextReplies.set(SID, context({ provider: "claude/enginer" }));

    render(SubAgentModal, { props: props(subAgent({ tokens_used: 0, max_tokens: 0 })) });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("not reported");
    });
  });

  /* Yoga's explicit call: this is a caption, not a control. */
  test("the strip offers nothing to click — provider and model are fixed at spawn", async () => {
    contextReplies.set(SID, context({ provider: "claude/enginer", model: "claude-opus-5" }));

    render(SubAgentModal, { props: props() });

    const el = await meter();
    await waitFor(() => expect(el.textContent).toContain("claude/enginer"));
    expect(el.querySelectorAll("button, select, input, a[href]").length).toBe(0);
  });

  /* A failed context call must not take the transcript down with it —
     the transcript is why the modal was opened. */
  test("a context call that fails leaves the strip honest and the modal usable", async () => {
    render(SubAgentModal, { props: props() });

    await waitFor(async () => {
      expect((await meter()).textContent).toContain("no reading yet");
    });
    expect(screen.getByText("Smoke test the sub-agent system")).toBeTruthy();
  });
});
