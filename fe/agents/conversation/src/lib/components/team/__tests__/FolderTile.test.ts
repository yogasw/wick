import { describe, test, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render } from "@testing-library/svelte";
import FolderTile from "../FolderTile.svelte";
import { rosterStatus } from "../../../rosterStatus.js";
import type { AgentItem } from "../../../api/team.js";

const agent = (id: string, p: Record<string, unknown> = {}) => ({ id, handle: id, name: id, status: "idle", disabled: false, main_session_id: `m-${id}`, ...p }) as unknown as AgentItem;
const member = (a: AgentItem) => ({ agent: a, st: rosterStatus(a), open: false });

/* The folder icon has no cue of its own: each face is the member's live
   roster avatar, in the member's own state. */
describe("FolderTile", () => {
  test("one avatar-activity per member, carrying the member's activity", () => {
    const members = [
      agent("tooling", { status: "running", current_action: "Bash" }),
      agent("asking", { needs_attention: true }),
      agent("remote", { status: "running", kind: "a2a-remote" }),
      agent("idle"),
      agent("fifth"),
    ].map(member);
    const r = render(FolderTile, { members });
    const acts = [...r.container.querySelectorAll<HTMLElement>("[data-testid=avatar-activity]")];
    expect(acts.map((e) => e.dataset.activity)).toEqual(["tool", "alert", "remote", "idle"]);
    const tile = r.getByTestId("roster-folder-icon");
    expect(tile.dataset.busy).toBeUndefined();
    expect([...tile.classList].some((c) => c.startsWith("roster-folder-"))).toBe(false);
    expect(r.container.querySelector("[data-testid=roster-folder-badge]")).toBeNull();
  });

  test("AgentsApp draws the icon through FolderTile with no folder-level busy cue", () => {
    const src = readFileSync(resolve(__dirname, "../../../../AgentsApp.svelte"), "utf8");
    expect(src).toMatch(/<FolderTile members=/);
    expect(src).not.toMatch(/roster-folder-badge|roster-folder-thinking|roster-folder-waiting|roster-hourglass|data-busy/);
  });
});
