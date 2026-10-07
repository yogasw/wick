import { describe, test, expect, vi, afterEach } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";
import FileViewerModal from "../FileViewerModal.svelte";
import type { FileContent } from "../../types/agents.js";

/* Escape over the side panel: the first press closes the file modal only,
   the second closes the panel. The panel is stood in for by the handler
   DetailView installs — a window listener that closes on any Escape nobody
   prevented — because that ordering (window listener registered first) is
   exactly what used to close both at once. */
function panelStandIn() {
  const panel = { open: true };
  const onKey = (e: KeyboardEvent) => {
    if (e.key !== "Escape" || e.defaultPrevented) return;
    if (panel.open) { e.preventDefault(); panel.open = false; }
  };
  window.addEventListener("keydown", onKey);
  return { panel, dispose: () => window.removeEventListener("keydown", onKey) };
}

const VIEW_FILE: FileContent = {
  path: "README.md", size: 12, binary: false, content: "# hi", tooBig: false, mtime: 1,
};
const EDIT_FILE: FileContent = {
  path: "src/main.go", size: 30, binary: false, content: "package main", tooBig: false, mtime: 1,
};

let dispose: (() => void) | null = null;
afterEach(() => { dispose?.(); dispose = null; });

describe.each([
  ["view", VIEW_FILE],
  ["edit", EDIT_FILE],
])("Escape over the panel — %s path", (_name, file) => {
  test("first Escape closes the modal only, the next closes the panel", async () => {
    const s = panelStandIn();
    dispose = s.dispose;
    const onClose = vi.fn();
    const { rerender, container } = render(FileViewerModal, {
      props: { file, dirty: false, onSave: vi.fn(), onClose },
    });
    // Nothing clicked in the modal: the key goes wherever focus is, and
    // opening the modal moved focus into it.
    const dialog = container.ownerDocument.querySelector("[role='dialog']");
    expect(dialog?.contains(document.activeElement)).toBe(true);

    await fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(s.panel.open).toBe(true);

    await rerender({ file: null, dirty: false, onSave: vi.fn(), onClose });
    await fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(s.panel.open).toBe(false);
  });
});
