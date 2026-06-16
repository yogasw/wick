import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SkillsList from "../SkillsList.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

const mockData = {
  dirs: ["~/.claude/skills", "~/.agents/skills"],
  skills: [
    { name: "brainstorming", is_dir: true, in_dirs: ["~/.claude/skills"], missing_dirs: ["~/.agents/skills"] },
    { name: "system-debug.md", is_dir: false, in_dirs: ["~/.claude/skills", "~/.agents/skills"], missing_dirs: [] },
  ],
};

beforeEach(() => {
  vi.mocked(api.listSkills).mockResolvedValue(mockData);
  vi.mocked(api.postMutation).mockResolvedValue(undefined);
});

describe("SkillsList", () => {
  it("renders skill names after load", async () => {
    render(SkillsList, { props: { onNavigate: vi.fn() } });
    expect(await screen.findByText("brainstorming/")).toBeTruthy();
    expect(screen.getByText("system-debug.md")).toBeTruthy();
  });

  it("shows dir presence badges", async () => {
    render(SkillsList, { props: { onNavigate: vi.fn() } });
    await screen.findByText("brainstorming/");
    const claudeBadges = screen.getAllByText("claude");
    expect(claudeBadges.length).toBeGreaterThan(0);
  });

  it("calls onNavigate when row is clicked", async () => {
    const onNavigate = vi.fn();
    render(SkillsList, { props: { onNavigate } });
    const cell = await screen.findByText("brainstorming/");
    fireEvent.click(cell.closest("tr")!);
    expect(onNavigate).toHaveBeenCalledWith("brainstorming");
  });

  it("shows synced status when no missing dirs", async () => {
    render(SkillsList, { props: { onNavigate: vi.fn() } });
    await screen.findByText("brainstorming/");
    expect(screen.getByText("synced")).toBeTruthy();
  });

  it("shows missing count when dirs missing", async () => {
    render(SkillsList, { props: { onNavigate: vi.fn() } });
    await screen.findByText("brainstorming/");
    expect(screen.getByText("missing 1 dir(s)")).toBeTruthy();
  });
});
