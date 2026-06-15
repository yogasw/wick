import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ContextPanel from "../ContextPanel.svelte";
import type { ContextFileEntry } from "../../types/agents.js";

const DIR: ContextFileEntry = {
  path: "src",
  name: "src",
  size: 0,
  isDir: true,
  mtime: 0,
};

const FILE_A: ContextFileEntry = {
  path: "src/main.go",
  name: "main.go",
  size: 1024,
  isDir: false,
  mtime: Date.now() - 60000,
};

const FILE_B: ContextFileEntry = {
  path: "README.md",
  name: "README.md",
  size: 512,
  isDir: false,
  mtime: Date.now() - 120000,
};

describe("ContextPanel", () => {
  test("renders cwd", () => {
    render(ContextPanel, {
      props: {
        cwd: "/home/agent/project",
        files: [],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.getByText("/home/agent/project")).toBeDefined();
  });

  test("renders file names from flat list", () => {
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [FILE_B],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.getByText("README.md")).toBeDefined();
  });

  test("renders directory names", () => {
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [DIR, FILE_A],
        search: "",
        openDirs: { src: true },
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.getByText("src")).toBeDefined();
  });

  test("shows empty state when files is empty", () => {
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.getByText(/empty/i)).toBeDefined();
  });

  test("shows no-matches state when search yields no results", () => {
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [FILE_B],
        search: "zzznomatch",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.getByText(/no matches/i)).toBeDefined();
  });

  test("search input calls onSearch with new value", async () => {
    const onSearch = vi.fn();
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [FILE_B],
        search: "",
        openDirs: {},
        onSearch,
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    const input = screen.getByPlaceholderText(/filter/i);
    await fireEvent.input(input, { target: { value: "main" } });
    expect(onSearch).toHaveBeenCalledWith("main");
  });

  test("clicking a dir row calls onToggleDir with path", async () => {
    const onToggleDir = vi.fn();
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [DIR, FILE_A],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir,
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    await fireEvent.click(screen.getByText("src"));
    expect(onToggleDir).toHaveBeenCalledWith("src");
  });

  test("clicking a file row calls onOpen with the entry", async () => {
    const onOpen = vi.fn();
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [FILE_B],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen,
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    await fireEvent.click(screen.getByText("README.md"));
    expect(onOpen).toHaveBeenCalledOnce();
    expect(onOpen).toHaveBeenCalledWith(FILE_B);
  });

  test("refresh button calls onRefresh", async () => {
    const onRefresh = vi.fn();
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh,
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    await fireEvent.click(screen.getByTitle("Refresh"));
    expect(onRefresh).toHaveBeenCalledOnce();
  });

  test("new-file button calls onNewFile", async () => {
    const onNewFile = vi.fn();
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile,
        onNewDir: vi.fn(),
      },
    });
    await fireEvent.click(screen.getByTitle("New file"));
    expect(onNewFile).toHaveBeenCalledOnce();
  });

  test("new-dir button calls onNewDir", async () => {
    const onNewDir = vi.fn();
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir,
      },
    });
    await fireEvent.click(screen.getByTitle("New folder"));
    expect(onNewDir).toHaveBeenCalledOnce();
  });

  test("children of a closed dir are not rendered", () => {
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [DIR, FILE_A],
        search: "",
        openDirs: {},
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.queryByText("main.go")).toBeNull();
  });

  test("children of an open dir are rendered", () => {
    render(ContextPanel, {
      props: {
        cwd: "/project",
        files: [DIR, FILE_A],
        search: "",
        openDirs: { src: true },
        onSearch: vi.fn(),
        onToggleDir: vi.fn(),
        onOpen: vi.fn(),
        onRefresh: vi.fn(),
        onNewFile: vi.fn(),
        onNewDir: vi.fn(),
      },
    });
    expect(screen.getByText("main.go")).toBeDefined();
  });
});
