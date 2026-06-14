import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SkillFileView from "../SkillFileView.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");

beforeEach(() => {
  vi.resetAllMocks();
});

describe("SkillFileView - file", () => {
  it("renders markdown content for a file response", async () => {
    vi.mocked(api.getSkillFile).mockResolvedValue({
      name: "system-map/agents/README.md",
      is_dir: false,
      content: "# Agents README",
      source_path: "/home/user/.claude/skills/system-map/agents/README.md",
      in_dirs: ["~/.claude/skills"],
      entries: [],
    });
    render(SkillFileView, {
      props: {
        folder: "system-map",
        file: "agents/README.md",
        onBack: vi.fn(),
        onOpenChild: vi.fn(),
      },
    });
    expect(await screen.findByText("Agents README")).toBeTruthy();
  });
});

describe("SkillFileView - directory", () => {
  it("renders entry table when is_dir is true", async () => {
    vi.mocked(api.getSkillFile).mockResolvedValue({
      name: "system-map/agents",
      is_dir: true,
      in_dirs: ["~/.claude/skills"],
      entries: [
        { name: "README.md", is_dir: false, in_dirs: ["~/.claude/skills"], missing_dirs: [] },
        { name: "sub", is_dir: true, in_dirs: ["~/.claude/skills"], missing_dirs: [] },
      ],
    });
    render(SkillFileView, {
      props: {
        folder: "system-map",
        file: "agents",
        onBack: vi.fn(),
        onOpenChild: vi.fn(),
      },
    });
    expect(await screen.findByText("README.md")).toBeTruthy();
    expect(screen.getByText("sub/")).toBeTruthy();
  });

  it("calls onOpenChild with nested path when clicking a child entry", async () => {
    const onOpenChild = vi.fn();
    vi.mocked(api.getSkillFile).mockResolvedValue({
      name: "system-map/agents",
      is_dir: true,
      in_dirs: ["~/.claude/skills"],
      entries: [
        { name: "README.md", is_dir: false, in_dirs: ["~/.claude/skills"], missing_dirs: [] },
      ],
    });
    render(SkillFileView, {
      props: {
        folder: "system-map",
        file: "agents",
        onBack: vi.fn(),
        onOpenChild,
      },
    });
    const row = await screen.findByText("README.md");
    await fireEvent.click(row.closest("tr")!);
    expect(onOpenChild).toHaveBeenCalledWith("agents/README.md");
  });

  it("shows empty message when directory has no entries", async () => {
    vi.mocked(api.getSkillFile).mockResolvedValue({
      name: "system-map/empty-dir",
      is_dir: true,
      in_dirs: ["~/.claude/skills"],
      entries: [],
    });
    render(SkillFileView, {
      props: {
        folder: "system-map",
        file: "empty-dir",
        onBack: vi.fn(),
        onOpenChild: vi.fn(),
      },
    });
    expect(await screen.findByText("Folder is empty.")).toBeTruthy();
  });
});
