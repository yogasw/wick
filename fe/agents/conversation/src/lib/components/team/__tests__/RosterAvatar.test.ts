import { describe, test, expect, vi, afterEach } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen } from "@testing-library/svelte";
import RosterAvatar from "../RosterAvatar.svelte";
import { rosterStatus } from "../../../rosterStatus.js";
import type { AgentItem } from "../../../api/team.js";

/* P39: the roster row's avatar, one animation per state. */
const agent = (p: Record<string, unknown> = {}) => ({ id: "a1", handle: "anton", name: "Anton", status: "idle", disabled: false, main_session_id: "m1", ...p }) as unknown as AgentItem;

function draw(a: AgentItem, open = false) {
  const r = render(RosterAvatar, { agent: a, st: rosterStatus(a), open });
  const root = screen.getByTestId("avatar-activity");
  const has = (id: string) => r.container.querySelector(`[data-testid=${id}]`) !== null;
  const state = r.container.querySelector("svg[data-state]")?.getAttribute("data-state");
  return { r, root, has, state };
}

afterEach(() => vi.unstubAllGlobals());

describe("RosterAvatar", () => {
  test("a remote's wait draws the dashed ring only, not the avatar's orbit too", () => {
    const d = draw(agent({ status: "running", kind: "a2a-remote" }));
    expect(d.root.dataset.activity).toBe("remote");
    expect(d.has("avatar-remote")).toBe(true);
    expect(d.state).not.toBe("orbit");
  });

  test("the open main chat keeps the avatar's own remote cue, no ring", () => {
    const d = draw(agent({ status: "running", kind: "a2a-remote" }), true);
    expect(d.root.dataset.activity).toBe("idle");
    expect(d.has("avatar-remote")).toBe(false);
    expect(d.state).toBe("orbit");
  });

  test("a question waiting pulses the amber ring", () => {
    const d = draw(agent({ needs_attention: true }));
    expect(d.root.dataset.activity).toBe("alert");
    expect(d.root.classList.contains("alert")).toBe(true);
  });

  test("a tool call is acted out by the live avatar alone: no wobble, no orbit ring", () => {
    const d = draw(agent({ status: "running", current_action: "Bash" }));
    expect(d.root.dataset.activity).toBe("tool");
    expect(d.has("avatar-orbit")).toBe(false);
    expect(d.root.querySelector(".av-body")!.classList.contains("wobble")).toBe(false);
    expect(d.state).not.toBe("idle");
  });
});

/* The roster rows render through RosterAvatar, silenced by the main chat
   only (not by another chat of the same agent). */
describe("roster wiring", () => {
  test("AgentsApp renders each row's avatar through RosterAvatar with mainChatOpen", () => {
    const src = readFileSync(resolve(__dirname, "../../../../AgentsApp.svelte"), "utf8");
    const row = src.slice(src.indexOf('data-testid="roster-agent"'));
    expect(row).toMatch(/^[\s\S]*?<RosterAvatar agent=\{a\} \{st\} open=\{mainChatOpen\(a, selected\?\.id, route\)\}/);
    expect(src).toMatch(/import \{ mainChatOpen \} from "\.\/lib\/avatarActivity\.js";/);
  });
});
