import { describe, test, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/svelte";
import DelegationBlock from "../components/team/DelegationBlock.svelte";
import TaskTray from "../components/team/TaskTray.svelte";
import { ROSTER_CACHE, readRosterCache, writeRosterCache, teamAgentsByHandle } from "../rosterCache.js";
import type { AgentItem } from "../api/team.js";
import type { TeamTaskItem } from "../../types/agents.js";

const SRC = "/api/team/agents/a1/avatar/image?v=0123456789abcdef";

const anton = {
  id: "a1", handle: "anton", name: "Anton", description: "", disabled: false,
  avatar: { kind: "image", shape: "circle", color: "#27b199", image: { v: "0123456789abcdef", aspect: 1, fit: "fill", x: 0, y: 0, zoom: 0, src: SRC } },
} as unknown as AgentItem;

function task(o: Partial<TeamTaskItem> = {}): TeamTaskItem {
  return {
    task_id: "t1", context_id: "c1", to_agent_id: "a1", to_handle: "anton", to_name: "Anton",
    title: "check the logs", state: "working", turns: 1, max_turns: 8,
    started_at: "2026-10-10T10:00:00Z", updated_at: "2026-10-10T10:00:00Z", age: "2m",
    ...o,
  };
}

const srcs = (el: HTMLElement) => [...el.querySelectorAll("img")].map((i) => i.getAttribute("src"));

describe("rosterCache", () => {
  beforeEach(() => sessionStorage.clear());

  test("reads back what the sidebar wrote, null when empty or broken", () => {
    expect(readRosterCache()).toBeNull();
    writeRosterCache({ agents: [anton], captain_id: "cap" });
    expect(readRosterCache()).toEqual({ agents: [anton], captain_id: "cap" });
    sessionStorage.setItem(ROSTER_CACHE, "{not json");
    expect(readRosterCache()).toBeNull();
    writeRosterCache({ agents: [] });
    expect(readRosterCache()).toBeNull();
  });

  test("the delegation chip draws the roster's versioned src from the cache alone", () => {
    writeRosterCache({ agents: [anton] });
    const agents = teamAgentsByHandle(readRosterCache()!.agents!);
    expect(agents.anton.image?.src).toBe(SRC);
    const { container } = render(DelegationBlock, { props: { tasks: [task()], agents, onAnswer: vi.fn() } });
    const got = srcs(container);
    expect(got.length).toBeGreaterThan(0);
    expect(new Set(got)).toEqual(new Set([SRC]));
  });

  test("the task tray avatar draws the same src, ?v= included", () => {
    const agents = teamAgentsByHandle([anton]);
    const { container } = render(TaskTray, { props: { tasks: [task()], agents, onAnswer: vi.fn(), onCancel: vi.fn(), onJump: vi.fn() } });
    const got = srcs(container);
    expect(got.length).toBeGreaterThan(0);
    expect(got.every((s) => s === SRC && s.includes("?v=0123456789abcdef"))).toBe(true);
  });
});
