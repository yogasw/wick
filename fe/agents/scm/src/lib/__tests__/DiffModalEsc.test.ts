import { describe, test, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";

vi.mock("$lib/git-actions", () => ({
  loadCompare: vi.fn(() => Promise.resolve({ original: "a", modified: "b" })),
  loadCommitCompare: vi.fn(() => Promise.resolve({ original: "a", modified: "b" })),
  saveFile: vi.fn(),
  langFor: () => "plaintext",
}));
vi.mock("$lib/components/MonacoView.svelte", async () => ({
  default: (await import("./stubs/Empty.svelte")).default,
}));

import DiffModal from "$lib/components/DiffModal.svelte";

// The diff path of "Escape over the panel": the Source Control diff opens
// over the side panel, whose own Escape handler is a window listener.
describe("DiffModal over the panel", () => {
  test("first Escape closes the diff only, the next closes the panel", async () => {
    const panel = { open: true };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      panel.open = false;
    };
    window.addEventListener("keydown", onKey);
    try {
      const onClose = vi.fn();
      const file = { path: "a.txt", status: "M", untracked: false } as never;
      const { unmount } = render(DiffModal, { props: { file, onClose } });
      await fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(panel.open).toBe(true);
      unmount();
      await fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
      expect(panel.open).toBe(false);
    } finally {
      window.removeEventListener("keydown", onKey);
    }
  });
});
