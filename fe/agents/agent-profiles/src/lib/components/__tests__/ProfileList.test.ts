/*
 * The workspace shipped a `test:unit` script and no tests, so vitest exited
 * 1 with "No test files found" and kept the whole-FE gate red on its own.
 * These cover what the list actually decides: the empty state, and the two
 * affordances that appear only for someone allowed to create.
 */
import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProfileList from "../ProfileList.svelte";
import type { AgentProfile } from "@wick-fe/common-api";

const profile = (over: Partial<AgentProfile> = {}): AgentProfile => ({
  id: "p1",
  project_id: "",
  key: "reviewer",
  name: "Reviewer",
  description: "",
  icon: "",
  provider: "claude/default",
  model: "sonnet",
  system_prompt: "",
  allowed_tag_ids: [],
  allowed_native_tools: [],
  strict_mcp: false,
  default_max_turns: 0,
  can_delegate: false,
  allow_take_over: false,
  disabled: false,
  locked: false,
  ...over,
});

const cbs = () => ({
  onselect: vi.fn(),
  oncreate: vi.fn(),
});

describe("ProfileList", () => {
  test("says so when there are no roles yet", () => {
    render(ProfileList, { props: { profiles: [], selectedID: "", canCreate: true, ...cbs() } });
    expect(screen.getByText("No sub-agent roles yet.")).toBeTruthy();
  });

  test("offers New only to someone who may create one", async () => {
    const c = cbs();
    render(ProfileList, { props: { profiles: [], selectedID: "", canCreate: true, ...c } });
    await fireEvent.click(screen.getByText("+ New"));
    expect(c.oncreate).toHaveBeenCalled();
  });

  // A read-only viewer sees the same list; what they must not see is the way
  // to add to it.
  test("hides New in the read-only view", () => {
    render(ProfileList, { props: { profiles: [], selectedID: "", canCreate: false, ...cbs() } });
    expect(screen.queryByText("+ New")).toBeNull();
  });

  test("renders a row per role", () => {
    const profiles = [profile(), profile({ id: "p2", key: "writer", name: "Writer" })];
    render(ProfileList, { props: { profiles, selectedID: "p1", canCreate: true, ...cbs() } });
    expect(screen.queryByText("No sub-agent roles yet.")).toBeNull();
    expect(document.querySelectorAll("li").length).toBe(2);
  });
});
