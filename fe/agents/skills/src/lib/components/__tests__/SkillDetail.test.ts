import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import SkillDetail from "../SkillDetail.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastError: vi.fn(),
}));

beforeEach(() => {
  vi.resetAllMocks();
});

describe("SkillDetail - folder", () => {
  it("renders folder entries", async () => {
    vi.mocked(api.getSkill).mockResolvedValue({
      name: "brainstorming",
      is_dir: true,
      in_dirs: ["~/.claude/skills"],
      missing_dirs: [],
      entries: [
        { name: "README.md", is_dir: false, in_dirs: ["~/.claude/skills"], missing_dirs: [] },
        { name: "visual-companion.md", is_dir: false, in_dirs: ["~/.claude/skills"], missing_dirs: [] },
      ],
    });
    render(SkillDetail, { props: { name: "brainstorming", onBack: vi.fn() } });
    expect(await screen.findByText("README.md")).toBeTruthy();
    expect(screen.getByText("visual-companion.md")).toBeTruthy();
  });
});

describe("SkillDetail - file", () => {
  it("renders markdown content", async () => {
    vi.mocked(api.getSkill).mockResolvedValue({
      name: "system-debug.md",
      is_dir: false,
      content: "# Debug Guide\n\nThis is the content.",
      source_path: "/home/user/.claude/skills/system-debug.md",
      in_dirs: ["~/.claude/skills"],
      missing_dirs: [],
    });
    render(SkillDetail, { props: { name: "system-debug.md", onBack: vi.fn() } });
    await screen.findByText("Debug Guide");
    expect(screen.getByText("Debug Guide")).toBeTruthy();
  });

  it("shows source path", async () => {
    vi.mocked(api.getSkill).mockResolvedValue({
      name: "system-debug.md",
      is_dir: false,
      content: "# Hello",
      source_path: "/home/user/.claude/skills/system-debug.md",
      in_dirs: ["~/.claude/skills"],
      missing_dirs: [],
    });
    render(SkillDetail, { props: { name: "system-debug.md", onBack: vi.fn() } });
    expect(await screen.findByText("/home/user/.claude/skills/system-debug.md")).toBeTruthy();
  });
});
