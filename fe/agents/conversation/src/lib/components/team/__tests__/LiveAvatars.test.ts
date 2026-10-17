import { describe, test, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render } from "@testing-library/svelte";
import { compile } from "svelte/compiler";
import RosterAvatar from "../RosterAvatar.svelte";
import GroupAvatars from "../../GroupAvatars.svelte";
import { rosterStatus } from "../../../rosterStatus.js";
import type { AgentItem, GroupMember } from "../../../api/team.js";

const agent = (id: string, p: Record<string, unknown> = {}) => ({ id, handle: id, name: id, status: "idle", disabled: false, main_session_id: `m-${id}`, ...p }) as unknown as AgentItem;
const member = (id: string) => ({ id, handle: id, name: id, avatar: {}, is_captain: false, disabled: false, max_hops: 0 }) as unknown as GroupMember;
const app = () => readFileSync(resolve(__dirname, "../../../../AgentsApp.svelte"), "utf8");

/* Every avatar that stands for an agent's live state goes through
   RosterAvatar, so a Captain working only through sub-agents animates on
   the mobile pins, the chat header and group faces as on its row. */
describe("live avatars", () => {
  test("a pin-sized avatar of an agent working only through sub-agents shows the cue", () => {
    const a = agent("captain", { subagents_working: ["wick-fixer"] });
    const r = render(RosterAvatar, { agent: a, st: rosterStatus(a), open: false, size: 54 });
    const act = r.getByTestId("avatar-activity");
    expect(act.dataset.activity).toBe("tool");
    expect(r.container.querySelector("svg[width]")?.getAttribute("width")).toBe("54");
    expect(r.container.querySelector("svg[data-state]")?.getAttribute("data-state")).not.toBe("idle");
  });

  test("the mobile pins and the chat header render through RosterAvatar", () => {
    const src = app();
    const pins = src.slice(src.indexOf('data-testid="roster-pins"'), src.indexOf("</div>", src.indexOf('data-testid="roster-pins"')));
    expect(pins).toMatch(/<RosterAvatar agent=\{a\} st=\{pst\} open=\{mainChatOpen\(a, selected\?\.id, route\)\}[^>]*size=\{54\}/);
    expect(pins).toMatch(/\{@const pst = statusOf\(a\)\}/);
    expect(src).toMatch(/<RosterAvatar agent=\{selected\} st=\{headerStatus\}[^>]*size=\{36\}/);
    expect(src).not.toMatch(/<AgentAvatar /);
  });

  test("AgentsApp still compiles (no test mounts it)", () => {
    expect(() => compile(app(), { filename: "AgentsApp.svelte" })).not.toThrow();
  });

  test("group faces animate like the members' rows", () => {
    const busy = agent("busy", { status: "running", current_action: "Bash" });
    const live = (id: string) => (id === "busy" ? { agent: busy, st: rosterStatus(busy) } : null);
    const r = render(GroupAvatars, { members: [member("busy"), member("stranger")], size: 17, live });
    const acts = [...r.container.querySelectorAll<HTMLElement>("[data-testid=avatar-activity]")];
    expect(acts.map((e) => e.dataset.activity)).toEqual(["tool"]);
    expect(r.container.querySelectorAll("svg[width='17']").length).toBe(2);
    expect(app()).toMatch(/<GroupAvatars [^>]*live=\{liveMember\}/);
    const gv = readFileSync(resolve(__dirname, "../../GroupView.svelte"), "utf8");
    expect(gv).toMatch(/<GroupAvatars [^>]*live=\{liveMember\}/);
  });
});
