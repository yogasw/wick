import { describe, test, expect, beforeEach } from "vitest";
import { get } from "svelte/store";
import { createThreadStore } from "../thread.js";
import type { ConversationTurn, AgentEvent } from "../../types/agents.js";

function makeTurn(overrides: Partial<ConversationTurn> = {}): ConversationTurn {
  return {
    turn_id: "t1",
    role: "user",
    agent: "main",
    provider: "",
    text: "hello",
    timestamp: 1000,
    truncated: false,
    interrupted: false,
    has_trace: false,
    events: [],
    attachments: [],
    ...overrides,
  };
}

function ev(type: string, extras: Partial<AgentEvent> = {}): AgentEvent {
  return { type, ...extras };
}

describe("createThreadStore", () => {
  let store: ReturnType<typeof createThreadStore>;

  beforeEach(() => {
    store = createThreadStore();
  });

  /* ── setHistory ─────────────────────────────────────────────────── */

  test("setHistory populates turns", () => {
    const turns = [makeTurn({ turn_id: "a" }), makeTurn({ turn_id: "b" })];
    store.setHistory(turns);
    expect(get(store.turns)).toHaveLength(2);
    expect(get(store.turns)[0].turn_id).toBe("a");
    expect(get(store.turns)[1].turn_id).toBe("b");
  });

  test("setHistory replaces previously set turns", () => {
    store.setHistory([makeTurn({ turn_id: "old" })]);
    store.setHistory([makeTurn({ turn_id: "new1" }), makeTurn({ turn_id: "new2" })]);
    const turns = get(store.turns);
    expect(turns).toHaveLength(2);
    expect(turns[0].turn_id).toBe("new1");
  });

  /* ── initial state ──────────────────────────────────────────────── */

  test("turns starts empty", () => {
    expect(get(store.turns)).toHaveLength(0);
  });

  test("live starts null", () => {
    expect(get(store.live)).toBeNull();
  });

  test("typing starts inactive", () => {
    expect(get(store.typing).active).toBe(false);
  });

  /* ── session_start ──────────────────────────────────────────────── */

  test("session_start sets typing active", () => {
    store.handleEvent(ev("session_start"));
    expect(get(store.typing).active).toBe(true);
  });

  /* ── text_delta ─────────────────────────────────────────────────── */

  test("first text_delta creates live turn with that text", () => {
    store.handleEvent(ev("text_delta", { data: "Hello" }));
    const live = get(store.live);
    expect(live).not.toBeNull();
    expect(live!.text).toBe("Hello");
    expect(live!.blocks).toHaveLength(0);
  });

  test("two text_deltas accumulate into live.text", () => {
    store.handleEvent(ev("text_delta", { data: "Hello" }));
    store.handleEvent(ev("text_delta", { data: " world" }));
    expect(get(store.live)!.text).toBe("Hello world");
  });

  test("text_delta keeps live.blocks intact", () => {
    store.handleEvent(ev("thinking", { data: "reasoning..." }));
    store.handleEvent(ev("text_delta", { data: "answer" }));
    expect(get(store.live)!.blocks).toHaveLength(1);
  });

  /* ── thinking ───────────────────────────────────────────────────── */

  test("thinking event pushes a thinking block to live", () => {
    store.handleEvent(ev("thinking", { data: "deep thought" }));
    const live = get(store.live);
    expect(live).not.toBeNull();
    expect(live!.blocks).toHaveLength(1);
    expect(live!.blocks[0]).toEqual({ kind: "thinking", text: "deep thought" });
  });

  test("thinking starts live if not yet open", () => {
    store.handleEvent(ev("thinking", { data: "hmm" }));
    expect(get(store.live)).not.toBeNull();
  });

  /* ── tool_use ───────────────────────────────────────────────────── */

  test("tool_use pushes a tool block to live", () => {
    store.handleEvent(ev("tool_use", {
      tool_use_id: "u1",
      tool_name: "bash",
      tool_input: '{"cmd":"ls"}',
      at: 100,
    }));
    const live = get(store.live);
    expect(live!.blocks).toHaveLength(1);
    const block = live!.blocks[0];
    expect(block.kind).toBe("tool");
    if (block.kind === "tool") {
      expect(block.toolUseId).toBe("u1");
      expect(block.toolName).toBe("bash");
      expect(block.toolInput).toBe('{"cmd":"ls"}');
      expect(block.startedAt).toBe(100);
      expect(block.result).toBeUndefined();
    }
  });

  /* ── tool_result ────────────────────────────────────────────────── */

  test("tool_result with matching tool_use_id updates the tool block", () => {
    store.handleEvent(ev("tool_use", {
      tool_use_id: "u1",
      tool_name: "bash",
      tool_input: "{}",
      at: 100,
    }));
    store.handleEvent(ev("tool_result", {
      tool_use_id: "u1",
      data: "ok",
      is_error: false,
      at: 200,
    }));
    const live = get(store.live);
    expect(live!.blocks).toHaveLength(1);
    const block = live!.blocks[0];
    if (block.kind === "tool") {
      expect(block.result).toBe("ok");
      expect(block.isError).toBe(false);
      expect(block.endedAt).toBe(200);
    }
  });

  test("tool_result is_error=true sets isError on block", () => {
    store.handleEvent(ev("tool_use", { tool_use_id: "u2", tool_name: "x", tool_input: "{}", at: 1 }));
    store.handleEvent(ev("tool_result", { tool_use_id: "u2", data: "boom", is_error: true, at: 2 }));
    const block = get(store.live)!.blocks[0];
    if (block.kind === "tool") {
      expect(block.isError).toBe(true);
    }
  });

  test("tool_result without matching tool_use_id appends standalone tool block", () => {
    store.handleEvent(ev("tool_result", {
      tool_use_id: "orphan",
      data: "standalone result",
      is_error: false,
      at: 300,
    }));
    const live = get(store.live);
    expect(live).not.toBeNull();
    expect(live!.blocks).toHaveLength(1);
    const block = live!.blocks[0];
    expect(block.kind).toBe("tool");
    if (block.kind === "tool") {
      expect(block.toolUseId).toBe("orphan");
      expect(block.result).toBe("standalone result");
      expect(block.isError).toBe(false);
      expect(block.endedAt).toBe(300);
    }
  });

  /* ── done ───────────────────────────────────────────────────────── */

  test("done with live text pushes assistant turn to turns and clears live", () => {
    store.handleEvent(ev("text_delta", { data: "final answer" }));
    store.handleEvent(ev("done"));
    expect(get(store.live)).toBeNull();
    const turns = get(store.turns);
    expect(turns).toHaveLength(1);
    expect(turns[0].role).toBe("assistant");
    expect(turns[0].text).toBe("final answer");
  });

  test("done sets typing inactive", () => {
    store.handleEvent(ev("session_start"));
    store.handleEvent(ev("text_delta", { data: "hi" }));
    store.handleEvent(ev("done"));
    expect(get(store.typing).active).toBe(false);
  });

  test("done with no live content does not push an empty turn", () => {
    store.handleEvent(ev("done"));
    expect(get(store.turns)).toHaveLength(0);
  });

  test("done appends to existing history turns", () => {
    store.setHistory([makeTurn({ turn_id: "u1", role: "user" })]);
    store.handleEvent(ev("text_delta", { data: "reply" }));
    store.handleEvent(ev("done"));
    const turns = get(store.turns);
    expect(turns).toHaveLength(2);
    expect(turns[1].role).toBe("assistant");
  });

  /* ── error ──────────────────────────────────────────────────────── */

  test("error finalizes live turn same as done", () => {
    store.handleEvent(ev("text_delta", { data: "partial" }));
    store.handleEvent(ev("error"));
    expect(get(store.live)).toBeNull();
    expect(get(store.turns)).toHaveLength(1);
    expect(get(store.typing).active).toBe(false);
  });

  test("error with no live content does not push empty turn", () => {
    store.handleEvent(ev("error"));
    expect(get(store.turns)).toHaveLength(0);
    expect(get(store.live)).toBeNull();
  });

  /* ── lifecycle ──────────────────────────────────────────────────── */

  test("lifecycle idle sets typing inactive", () => {
    store.handleEvent(ev("session_start"));
    store.handleEvent(ev("lifecycle", { lifecycle: "idle" }));
    expect(get(store.typing).active).toBe(false);
  });

  test("lifecycle killed sets typing inactive", () => {
    store.handleEvent(ev("session_start"));
    store.handleEvent(ev("lifecycle", { lifecycle: "killed" }));
    expect(get(store.typing).active).toBe(false);
  });

  test("lifecycle working sets typing active and substate from data", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "working", data: "bash" }));
    const typing = get(store.typing);
    expect(typing.active).toBe(true);
    expect(typing.substate).toBe("bash");
  });

  test("lifecycle spawning sets typing active and substate=spawning", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "spawning" }));
    const typing = get(store.typing);
    expect(typing.active).toBe(true);
    expect(typing.substate).toBe("spawning");
  });

  test("lifecycle working with empty data sets substate to empty string", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "working", data: "" }));
    expect(get(store.typing).substate).toBe("");
  });

  /* ── system_turn ────────────────────────────────────────────────── */

  test("system_turn appends a system role turn to turns", () => {
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Agent spawned", steps: [] }) }));
    const turns = get(store.turns);
    expect(turns).toHaveLength(1);
    expect(turns[0].role).toBe("system");
    expect(turns[0].text).toBe("Agent spawned");
  });

  test("system_turn with steps includes them in events", () => {
    store.handleEvent(ev("system_turn", {
      data: JSON.stringify({ text: "done", steps: ["step A", "step B"] }),
    }));
    const turn = get(store.turns)[0];
    expect(turn.events.length).toBeGreaterThanOrEqual(2);
    const stepsInEvents = turn.events.filter((e) => e.type === "step");
    expect(stepsInEvents).toHaveLength(2);
    expect(stepsInEvents[0].text).toBe("step A");
  });

  test("system_turn appended after existing turns", () => {
    store.setHistory([makeTurn({ turn_id: "u0" })]);
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "notice", steps: [] }) }));
    expect(get(store.turns)).toHaveLength(2);
  });

  test("back-to-back provider switches collapse to the latest one", () => {
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Provider switched → codex", steps: [] }) }));
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Provider switched → claude/gemini", steps: [] }) }));
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Provider switched → claude/new", steps: [] }) }));
    const turns = get(store.turns);
    expect(turns).toHaveLength(1);
    expect(turns[0].text).toBe("Provider switched → claude/new");
  });

  test("a real turn between switches stops the collapse", () => {
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Provider switched → codex", steps: [] }) }));
    store.setHistory([
      ...get(store.turns),
      makeTurn({ turn_id: "chat-1", role: "assistant", text: "hi" }),
    ]);
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Provider switched → claude/new", steps: [] }) }));
    const turns = get(store.turns);
    // switch1 + chat + switch2 — the chat broke the run, nothing collapsed.
    expect(turns).toHaveLength(3);
  });

  test("a non-switch system turn does not collapse prior switches", () => {
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Provider switched → codex", steps: [] }) }));
    store.handleEvent(ev("system_turn", { data: JSON.stringify({ text: "Agent spawned", steps: [] }) }));
    expect(get(store.turns)).toHaveLength(2);
  });

  /* ── session_meta ───────────────────────────────────────────────── */

  test("session_meta updates meta.title", () => {
    store.handleEvent(ev("session_meta", {
      data: JSON.stringify({ session_id: "s1", title: "My Project" }),
    }));
    expect(get(store.meta).title).toBe("My Project");
  });

  test("session_meta with empty title does not overwrite existing title", () => {
    store.handleEvent(ev("session_meta", { data: JSON.stringify({ session_id: "s1", title: "First" }) }));
    store.handleEvent(ev("session_meta", { data: JSON.stringify({ session_id: "s1", title: "" }) }));
    expect(get(store.meta).title).toBe("First");
  });

  test("session_meta with no title field does not overwrite existing title", () => {
    store.handleEvent(ev("session_meta", { data: JSON.stringify({ session_id: "s1", title: "Keep me" }) }));
    store.handleEvent(ev("session_meta", { data: JSON.stringify({ session_id: "s1" }) }));
    expect(get(store.meta).title).toBe("Keep me");
  });

  /* ── finalize: thinking blocks in events ───────────────────────── */

  test("done after thinking event includes thinking entry in finalized turn events", () => {
    store.handleEvent(ev("thinking", { data: "my reasoning" }));
    store.handleEvent(ev("text_delta", { data: "answer" }));
    store.handleEvent(ev("done"));
    const turn = get(store.turns)[0];
    const thinkingEntries = turn.events.filter((e) => e.type === "thinking");
    expect(thinkingEntries).toHaveLength(1);
    expect(thinkingEntries[0].text).toBe("my reasoning");
  });

  test("done after thinking event sets has_trace to true", () => {
    store.handleEvent(ev("thinking", { data: "ponder" }));
    store.handleEvent(ev("text_delta", { data: "ok" }));
    store.handleEvent(ev("done"));
    expect(get(store.turns)[0].has_trace).toBe(true);
  });

  test("done with only thinking block (no text) still finalizes turn with thinking in events", () => {
    store.handleEvent(ev("thinking", { data: "silent reasoning" }));
    store.handleEvent(ev("done"));
    const turns = get(store.turns);
    expect(turns).toHaveLength(1);
    const thinkingEntries = turns[0].events.filter((e) => e.type === "thinking");
    expect(thinkingEntries).toHaveLength(1);
    expect(thinkingEntries[0].text).toBe("silent reasoning");
  });

  test("done with thinking + tool blocks includes both in finalized turn events", () => {
    store.handleEvent(ev("thinking", { data: "think first" }));
    store.handleEvent(ev("tool_use", { tool_use_id: "u1", tool_name: "bash", tool_input: "{}", at: 1 }));
    store.handleEvent(ev("tool_result", { tool_use_id: "u1", data: "output", is_error: false, at: 2 }));
    store.handleEvent(ev("text_delta", { data: "result" }));
    store.handleEvent(ev("done"));
    const turn = get(store.turns)[0];
    const thinkingEntries = turn.events.filter((e) => e.type === "thinking");
    const toolEntries = turn.events.filter((e) => e.type === "tool_use");
    expect(thinkingEntries).toHaveLength(1);
    expect(toolEntries).toHaveLength(1);
    expect(toolEntries[0].tool_name).toBe("bash");
  });

  /* ── ignored event types ────────────────────────────────────────── */

  test("approval_request is ignored without side-effects", () => {
    store.handleEvent(ev("approval_request", { data: '{"id":"a1"}' }));
    expect(get(store.turns)).toHaveLength(0);
    expect(get(store.live)).toBeNull();
  });

  test("ask_user is ignored without side-effects", () => {
    store.handleEvent(ev("ask_user", { data: '{"id":"q1"}' }));
    expect(get(store.turns)).toHaveLength(0);
    expect(get(store.live)).toBeNull();
  });

  /* ── appendUserTurn ─────────────────────────────────────────────── */

  test("appendUserTurn appends a user turn with correct role and text", () => {
    store.appendUserTurn("hi");
    const turns = get(store.turns);
    expect(turns).toHaveLength(1);
    expect(turns[0].role).toBe("user");
    expect(turns[0].text).toBe("hi");
  });

  test("appendUserTurn sets events to empty array", () => {
    store.appendUserTurn("hi");
    expect(get(store.turns)[0].events).toEqual([]);
  });

  test("appendUserTurn sets attachments to empty array when none provided", () => {
    store.appendUserTurn("hi");
    expect(get(store.turns)[0].attachments).toEqual([]);
  });

  test("appendUserTurn uses provided attachments", () => {
    const att = [{ name: "file.txt", stored_name: "abc.txt", url: "/f/abc", mime: "text/plain", size: 10 }];
    store.appendUserTurn("with attachment", att);
    expect(get(store.turns)[0].attachments).toEqual(att);
  });

  test("appendUserTurn generates unique turn_id per call", () => {
    store.appendUserTurn("first");
    store.appendUserTurn("second");
    const turns = get(store.turns);
    expect(turns[0].turn_id).not.toBe(turns[1].turn_id);
    expect(turns[0].turn_id.startsWith("local-user-")).toBe(true);
    expect(turns[1].turn_id.startsWith("local-user-")).toBe(true);
  });

  test("appendUserTurn appends after existing history turns", () => {
    store.setHistory([makeTurn({ turn_id: "hist-1" })]);
    store.appendUserTurn("new message");
    const turns = get(store.turns);
    expect(turns).toHaveLength(2);
    expect(turns[1].text).toBe("new message");
  });

  /* ── lifecycle store ────────────────────────────────────────────── */

  test("lifecycle starts with empty state", () => {
    expect(get(store.lifecycle).state).toBe("");
    expect(get(store.lifecycle).pid).toBe(0);
  });

  test("handleEvent lifecycle:working sets lifecycle store to working with pid", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "working", pid: 123, data: "bash", at: 500 }));
    const lc = get(store.lifecycle);
    expect(lc.state).toBe("working");
    expect(lc.pid).toBe(123);
    expect(lc.substate).toBe("bash");
    expect(lc.at).toBe(500);
  });

  test("handleEvent lifecycle:idle sets lifecycle store to idle", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 42, at: 100 }));
    const lc = get(store.lifecycle);
    expect(lc.state).toBe("idle");
    expect(lc.pid).toBe(42);
  });

  test("handleEvent lifecycle:killed sets lifecycle store to killed", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "killed", pid: 99, at: 200 }));
    const lc = get(store.lifecycle);
    expect(lc.state).toBe("killed");
    expect(lc.pid).toBe(99);
  });

  test("handleEvent lifecycle:spawning sets lifecycle store to spawning", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "spawning", pid: 7, at: 300 }));
    const lc = get(store.lifecycle);
    expect(lc.state).toBe("spawning");
    expect(lc.pid).toBe(7);
  });

  /* ── lifecycle nudge from content stream ────────────────────────── */

  test("text_delta nudges lifecycle state to working when state is idle", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 1, at: 10 }));
    store.handleEvent(ev("text_delta", { data: "hello" }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("text_delta nudges lifecycle state to working when state is empty string", () => {
    store.handleEvent(ev("text_delta", { data: "hello" }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("text_delta nudges lifecycle state to working when spawning", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "spawning", pid: 5, at: 1 }));
    store.handleEvent(ev("text_delta", { data: "hello" }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("text_delta does NOT change lifecycle state when killed", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "killed", pid: 5, at: 1 }));
    store.handleEvent(ev("text_delta", { data: "hello" }));
    expect(get(store.lifecycle).state).toBe("killed");
  });

  test("text_delta nudge preserves pid, substate, at on lifecycle", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 42, data: "sub", at: 999 }));
    store.handleEvent(ev("text_delta", { data: "hello" }));
    const lc = get(store.lifecycle);
    expect(lc.state).toBe("working");
    expect(lc.pid).toBe(42);
    expect(lc.substate).toBe("sub");
    expect(lc.at).toBe(999);
  });

  test("thinking nudges lifecycle state to working when state is idle", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 1, at: 10 }));
    store.handleEvent(ev("thinking", { data: "reasoning" }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("thinking nudges lifecycle state to working when spawning", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "spawning", pid: 5, at: 1 }));
    store.handleEvent(ev("thinking", { data: "reasoning" }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("tool_use nudges lifecycle state to working when state is idle", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 1, at: 10 }));
    store.handleEvent(ev("tool_use", { tool_use_id: "u1", tool_name: "bash", tool_input: "{}", at: 1 }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("tool_use nudges lifecycle state to working when spawning", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "spawning", pid: 5, at: 1 }));
    store.handleEvent(ev("tool_use", { tool_use_id: "u1", tool_name: "bash", tool_input: "{}", at: 1 }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("tool_result nudges lifecycle state to working when state is idle", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 1, at: 10 }));
    store.handleEvent(ev("tool_result", { tool_use_id: "u1", data: "ok", is_error: false, at: 2 }));
    expect(get(store.lifecycle).state).toBe("working");
  });

  test("tool_result does NOT change lifecycle state when killed", () => {
    store.handleEvent(ev("lifecycle", { lifecycle: "killed", pid: 5, at: 1 }));
    store.handleEvent(ev("tool_result", { tool_use_id: "u1", data: "ok", is_error: false, at: 2 }));
    expect(get(store.lifecycle).state).toBe("killed");
  });

  test("explicit lifecycle:idle event still sets state to idle after content nudge", () => {
    store.handleEvent(ev("text_delta", { data: "hello" }));
    expect(get(store.lifecycle).state).toBe("working");
    store.handleEvent(ev("lifecycle", { lifecycle: "idle", pid: 1, at: 20 }));
    expect(get(store.lifecycle).state).toBe("idle");
  });
});
