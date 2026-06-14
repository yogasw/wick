import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import FileViewerModal from "../FileViewerModal.svelte";
import type { FileContent } from "../../types/agents.js";

const TEXT_FILE: FileContent = {
  path: "src/main.go",
  size: 256,
  binary: false,
  content: "package main\n\nfunc main() {}",
  tooBig: false,
  mtime: 1700000000,
};

const BINARY_FILE: FileContent = {
  path: "assets/logo.png",
  size: 8192,
  binary: true,
  tooBig: false,
  mtime: 1700000000,
};

const TOOBIG_FILE: FileContent = {
  path: "dump.sql",
  size: 104857600,
  binary: false,
  tooBig: true,
  mtime: 1700000000,
};

describe("FileViewerModal", () => {
  test("renders nothing when file is null", () => {
    const { container } = render(FileViewerModal, {
      props: { file: null, dirty: false, onSave: vi.fn(), onClose: vi.fn() },
    });
    expect(container.querySelector("[data-testid='file-viewer']")).toBeNull();
  });

  test("renders file path", () => {
    render(FileViewerModal, {
      props: { file: TEXT_FILE, dirty: false, onSave: vi.fn(), onClose: vi.fn() },
    });
    expect(screen.getByText("src/main.go")).toBeDefined();
  });

  test("renders text content in textarea", () => {
    render(FileViewerModal, {
      props: { file: TEXT_FILE, dirty: false, onSave: vi.fn(), onClose: vi.fn() },
    });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    expect(textarea.value).toBe("package main\n\nfunc main() {}");
  });

  test("editing textarea and clicking Save calls onSave with new content", async () => {
    const onSave = vi.fn();
    render(FileViewerModal, {
      props: { file: TEXT_FILE, dirty: false, onSave, onClose: vi.fn() },
    });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "package main\n" } });
    await fireEvent.click(screen.getByText("Save"));
    expect(onSave).toHaveBeenCalledOnce();
    expect(onSave).toHaveBeenCalledWith("package main\n");
  });

  test("Close button calls onClose", async () => {
    const onClose = vi.fn();
    render(FileViewerModal, {
      props: { file: TEXT_FILE, dirty: false, onSave: vi.fn(), onClose },
    });
    await fireEvent.click(screen.getByTitle("Close"));
    expect(onClose).toHaveBeenCalledOnce();
  });

  test("binary file shows notice instead of textarea", () => {
    render(FileViewerModal, {
      props: { file: BINARY_FILE, dirty: false, onSave: vi.fn(), onClose: vi.fn() },
    });
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.getByText(/binary/i)).toBeDefined();
  });

  test("tooBig file shows notice instead of textarea", () => {
    render(FileViewerModal, {
      props: { file: TOOBIG_FILE, dirty: false, onSave: vi.fn(), onClose: vi.fn() },
    });
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.getByText(/too large/i)).toBeDefined();
  });

  test("download link uses downloadHref when provided", () => {
    const { container } = render(FileViewerModal, {
      props: {
        file: TEXT_FILE,
        dirty: false,
        onSave: vi.fn(),
        onClose: vi.fn(),
        downloadHref: "/sessions/sess-1/files/download?path=src%2Fmain.go",
      },
    });
    const link = container.querySelector("a[download]") as HTMLAnchorElement;
    expect(link).toBeDefined();
    expect(link.href).toContain("download");
  });

  test("does not show download link when downloadHref is absent", () => {
    const { container } = render(FileViewerModal, {
      props: { file: TEXT_FILE, dirty: false, onSave: vi.fn(), onClose: vi.fn() },
    });
    expect(container.querySelector("a[download]")).toBeNull();
  });
});
