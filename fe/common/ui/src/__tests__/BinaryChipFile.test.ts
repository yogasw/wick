import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup, waitFor } from "@testing-library/svelte";
import TraceBody from "../trace/TraceBody.svelte";
import { traceFileState } from "../trace/fileState.js";
import type { TraceContext, TraceDisplay, TraceFileStat } from "../trace/types.js";

afterEach(() => cleanup());

// A Read of a screenshot whose trace kept no bytes (no data, no blob_ref).
const d: TraceDisplay = { kind: "image", mime: "image/png", original_bytes: 8192, name: "shot.png" };
const PATH = "/proj/files/shots/shot.png";
const CALLED = Date.parse("2026-10-03T16:31:20Z");

function ctx(st: TraceFileStat, over: Partial<TraceContext> = {}): TraceContext {
  return {
    sourcePath: PATH,
    calledAt: CALLED,
    statPath: vi.fn(async () => st),
    loadPath: vi.fn(async () => new Blob(["png"], { type: "image/png" })),
    onOpenMedia: vi.fn(),
    ...over,
  };
}

async function chip(c: TraceContext, display = d): Promise<HTMLButtonElement> {
  const { container } = render(TraceBody, { display, ctx: c });
  const el = container.querySelector("[data-binary-chip]") as HTMLButtonElement;
  await waitFor(() => expect(el.dataset.fileState).not.toBe("checking"));
  return el;
}

describe("traceFileState", () => {
  it("same: present and not written after the call", () => {
    expect(traceFileState({ status: "present", size: 8192, mtime: CALLED - 60_000 }, 8192, CALLED).state).toBe("same");
  });
  it("changed: written after the call, sizes in the title", () => {
    const s = traceFileState({ status: "present", size: 9216, mtime: CALLED + 120_000 }, 8192, CALLED);
    expect(s.state).toBe("changed");
    expect(s.label).toBe("diperbarui setelah trace ini");
    expect(s.title).toContain("8 KB tercatat, sekarang 9 KB");
  });
  it("a size difference alone does not claim a change when the mtime says otherwise", () => {
    expect(traceFileState({ status: "present", size: 9216, mtime: CALLED - 1000 }, 8192, CALLED).state).toBe("same");
  });
  it("no call time: falls back to the size it can prove", () => {
    expect(traceFileState({ status: "present", size: 9216, mtime: 1 }, 8192).state).toBe("changed");
    expect(traceFileState({ status: "present", size: 9216, mtime: 1 }).state).toBe("same");
  });
  it("missing and unknown", () => {
    expect(traceFileState({ status: "missing" }).label).toBe("sudah dihapus");
    expect(traceFileState({ status: "unknown" }).label).toBe("tidak bisa dicek");
    expect(traceFileState(null).state).toBe("unknown");
  });
});

describe("BinaryChip falls back to the file a call named", () => {
  it("same: clickable, loads from the session file and opens the viewer", async () => {
    const c = ctx({ status: "present", rel: "shots/shot.png", size: 8192, mtime: CALLED - 1000 });
    const el = await chip(c);
    expect(c.statPath).toHaveBeenCalledWith(PATH);
    expect(el.dataset.fileState).toBe("same");
    expect(el.disabled).toBe(false);
    expect(el.title).toBe("Click to load from file");
    expect(el.textContent).toContain("shot.png · PNG · 8 KB");
    globalThis.URL.createObjectURL = vi.fn(() => "blob:f");
    globalThis.URL.revokeObjectURL = vi.fn();
    await fireEvent.click(el);
    await waitFor(() => expect(c.loadPath).toHaveBeenCalledWith("shots/shot.png"));
    const img = await waitFor(() => el.parentElement!.querySelector("img")!);
    await fireEvent.click(img.closest("button")!);
    expect(c.onOpenMedia).toHaveBeenCalledWith(expect.objectContaining({ url: "blob:f", kind: "image" }));
  });

  it("changed: says so, stays clickable, and the preview is marked as the current version", async () => {
    const c = ctx({ status: "present", rel: "shots/shot.png", size: 9216, mtime: CALLED + 120_000 });
    const el = await chip(c);
    expect(el.dataset.fileState).toBe("changed");
    expect(el.textContent).toContain("shot.png · PNG · 8 KB · diperbarui setelah trace ini");
    expect(el.title).toContain("8 KB tercatat, sekarang 9 KB");
    expect(el.disabled).toBe(false);
    globalThis.URL.createObjectURL = vi.fn(() => "blob:g");
    await fireEvent.click(el);
    const note = await waitFor(() => el.parentElement!.querySelector("[data-binary-note]")!);
    expect(note.textContent).toBe("Ini versi sekarang, bukan versi saat trace");
    await fireEvent.click(el.parentElement!.querySelector("img")!.closest("button")!);
    expect(c.onOpenMedia).toHaveBeenCalledWith(expect.objectContaining({ note: "Ini versi sekarang, bukan versi saat trace" }));
  });

  it("deleted: disabled, keeps the recorded size", async () => {
    const c = ctx({ status: "missing" });
    const el = await chip(c);
    expect(el.dataset.fileState).toBe("missing");
    expect(el.disabled).toBe(true);
    expect(el.textContent).toContain("shot.png · PNG · 8 KB · sudah dihapus");
    expect(el.title).toBe("File sudah dihapus dari disk");
  });

  it("outside the session folder: cannot be checked, no claim", async () => {
    const el = await chip(ctx({ status: "unknown" }));
    expect(el.dataset.fileState).toBe("unknown");
    expect(el.disabled).toBe(true);
    expect(el.textContent).toContain("· tidak bisa dicek");
    expect(el.textContent).not.toContain("dihapus");
  });

  it("a file deleted between the check and the click turns into deleted, not an error", async () => {
    const c = ctx({ status: "present", rel: "shots/shot.png", size: 8192, mtime: CALLED }, {
      loadPath: vi.fn(async () => { throw new Error("file shots/shot.png: 404"); }),
    });
    const el = await chip(c);
    await fireEvent.click(el);
    await waitFor(() => expect(el.dataset.fileState).toBe("missing"));
    expect(el.parentElement!.textContent).not.toContain("failed to load");
  });

  it("no data, no blob and no path: still 'Not stored in the trace'", async () => {
    const { container } = render(TraceBody, { display: d, ctx: { onOpenMedia: vi.fn() } });
    const el = container.querySelector("[data-binary-chip]") as HTMLButtonElement;
    expect(el.disabled).toBe(true);
    expect(el.title).toBe("Not stored in the trace");
    expect(el.dataset.fileState).toBeUndefined();
  });

  it("a stored blob wins over the path; a swept blob reads as deleted output", async () => {
    const statPath = vi.fn();
    const loadBlob = vi.fn(async () => { throw new Error("blob e1: 404"); });
    const { container } = render(TraceBody, { display: { ...d, blob_ref: "e1" }, ctx: { ...ctx({ status: "present" }), statPath, loadBlob } });
    const el = container.querySelector("[data-binary-chip]") as HTMLButtonElement;
    expect(statPath).not.toHaveBeenCalled();
    await fireEvent.click(el);
    await waitFor(() => expect(el.textContent).toContain("output trace sudah dihapus"));
    expect(el.disabled).toBe(true);
  });
});
