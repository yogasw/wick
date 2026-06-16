import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import FileTreeNode from "../FileTreeNode.svelte";
import type { ContextFileEntry } from "../../types/agents.js";

function fileNode(over: Partial<ContextFileEntry> = {}) {
  return { entry: { path: "src/a.ts", name: "a.ts", isDir: false, size: 2048, mtime: Date.now(), ...over }, children: [] };
}

const base = {
  depth: 0,
  forceOpen: false,
  openDirs: {},
  onToggleDir: vi.fn(),
  onOpen: vi.fn(),
  onDownload: vi.fn(),
  onDelete: vi.fn(),
  onNewHere: vi.fn(),
};

describe("FileTreeNode - file row metadata", () => {
  test("shows size · time subline", () => {
    render(FileTreeNode, { props: { node: fileNode(), ...base } });
    expect(screen.getByText(/2\.0 KB/)).toBeDefined();
  });

  test("download button calls onDownload with path", async () => {
    const onDownload = vi.fn();
    render(FileTreeNode, { props: { node: fileNode(), ...base, onDownload } });
    await fireEvent.click(screen.getByTitle("Download"));
    expect(onDownload).toHaveBeenCalledWith("src/a.ts");
  });

  test("delete button calls onDelete with path", async () => {
    const onDelete = vi.fn();
    render(FileTreeNode, { props: { node: fileNode(), ...base, onDelete } });
    await fireEvent.click(screen.getByTitle("Delete"));
    expect(onDelete).toHaveBeenCalledWith("src/a.ts");
  });
});
