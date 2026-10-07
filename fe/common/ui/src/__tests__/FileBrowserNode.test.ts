import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import FileBrowserNode from "../FileBrowserNode.svelte";
import type { SessionFileEntry } from "../file-browser-types.js";

function fileNode(over: Partial<SessionFileEntry> = {}) {
  return { entry: { path: "src/a.ts", name: "a.ts", isDir: false, size: 2048, mtime: Date.now(), ...over }, children: [] };
}

function dirNode(over: Partial<SessionFileEntry> = {}) {
  return { entry: { path: "src", name: "src", isDir: true, size: 0, mtime: Date.now(), ...over }, children: [] };
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

/** Open the row's ⋮ menu and click one of its items. */
async function pick(label: string) {
  await fireEvent.click(screen.getByRole("button", { name: /Actions for/ }));
  await fireEvent.click(screen.getByRole("menuitem", { name: label }));
}

describe("FileBrowserNode - file row", () => {
  test("shows size · time subline", () => {
    render(FileBrowserNode, { props: { node: fileNode(), ...base } });
    expect(screen.getByText(/2\.0 KB/)).toBeDefined();
  });

  test("download is reachable from the row menu", async () => {
    const onDownload = vi.fn();
    render(FileBrowserNode, { props: { node: fileNode(), ...base, onDownload } });
    await pick("Download");
    expect(onDownload).toHaveBeenCalledWith("src/a.ts");
  });

  test("delete is reachable from the row menu", async () => {
    const onDelete = vi.fn();
    render(FileBrowserNode, { props: { node: fileNode(), ...base, onDelete } });
    await pick("Delete file");
    expect(onDelete).toHaveBeenCalledWith("src/a.ts");
  });
});

describe("FileBrowserNode - folder row", () => {
  test("new file and delete are in the menu", async () => {
    const onNewHere = vi.fn();
    render(FileBrowserNode, { props: { node: dirNode(), ...base, onNewHere } });
    await pick("New file here");
    expect(onNewHere).toHaveBeenCalledWith("src");
  });

  test("new folder appears only when the caller offers it", async () => {
    render(FileBrowserNode, { props: { node: dirNode(), ...base } });
    await fireEvent.click(screen.getByRole("button", { name: /Actions for folder/ }));
    // A dead row is worse than a missing one: a caller with no folder
    // creation must not advertise it.
    expect(screen.queryByRole("menuitem", { name: "New folder here" })).toBeNull();
  });

  test("new folder calls back with the folder's own path", async () => {
    const onNewDirHere = vi.fn();
    render(FileBrowserNode, { props: { node: dirNode(), ...base, onNewDirHere } });
    await pick("New folder here");
    expect(onNewDirHere).toHaveBeenCalledWith("src");
  });

  test("opening the menu does not toggle the folder", async () => {
    const onToggleDir = vi.fn();
    render(FileBrowserNode, { props: { node: dirNode(), ...base, onToggleDir } });
    // The whole left half of a folder row opens it. The actions used to sit
    // on top of that as absolutely positioned icons, so a click that missed
    // one by a pixel opened the folder instead of acting on it.
    await fireEvent.click(screen.getByRole("button", { name: /Actions for folder/ }));
    expect(onToggleDir).not.toHaveBeenCalled();
  });
});
