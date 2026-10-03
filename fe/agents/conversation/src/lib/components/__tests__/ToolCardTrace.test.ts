import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";
import { tick } from "svelte";
import ToolCard from "../ToolCard.svelte";
import type { ThreadBlock } from "../../types/agents.js";

type ToolBlock = Extract<ThreadBlock, { kind: "tool" }>;
afterEach(() => cleanup());

const base: ToolBlock = { kind: "tool", toolUseId: "t1", toolName: "Bash", toolInput: "", hasResult: true };

async function openResult(container: HTMLElement) {
  const btns = container.querySelectorAll("button");
  // The result toggle is the button labelled result/error.
  const res = Array.from(btns).find((b) => /result|error/i.test(b.textContent ?? ""));
  await fireEvent.click(res!);
}

describe("ToolCard renders trace bodies through TraceBody", () => {
  it("escaped markdown result renders as markdown (no display: TS classifier)", async () => {
    const { container } = render(ToolCard, {
      block: { ...base, toolName: "Agent", result: JSON.stringify("# P1 Laporan\n\n- **satu**: ok") },
    });
    await openResult(container);
    const md = container.querySelector("[data-trace-kind=markdown]");
    expect(md?.textContent).toContain("P1 Laporan");
    expect(md?.textContent).not.toContain("\\n");
  });

  it("bash call shows a command card from the backend display", async () => {
    const { container } = render(ToolCard, {
      block: {
        ...base,
        toolInput: '{"command":"git status"}',
        inputDisplay: { kind: "command", command: "git status", lang: "shell", summary: "Show status" },
        result: "fatal: not a git repository",
        resultDisplay: { kind: "terminal", body: "fatal: not a git repository", exit_code: 128 },
      },
    });
    expect(container.querySelector("[data-tool-description]")?.textContent).toBe("Show status");
    await fireEvent.click(container.querySelector("button")!);
    expect(container.querySelector("[data-trace-kind=command]")?.textContent).toContain("$ git status");
    await openResult(container);
    expect(container.querySelector("[data-exit-code]")?.textContent).toBe("exit 128");
  });

  it("image result is a chip; the blob is fetched only on click", async () => {
    globalThis.URL.createObjectURL = vi.fn(() => "blob:z");
    globalThis.URL.revokeObjectURL = vi.fn();
    const loadBlob = vi.fn(async () => new Blob(["x"], { type: "image/png" }));
    const { container } = render(ToolCard, {
      block: {
        ...base,
        toolName: "Read",
        result: "[image/png · 512 KB · blob e1]",
        resultDisplay: { kind: "image", mime: "image/png", original_bytes: 524288, name: "shot.png", summary: "PNG · 512 KB", blob_ref: "e1" },
      },
      loadBlob,
    });
    expect(container.textContent).toContain("shot.png · PNG · 512 KB");
    await openResult(container);
    expect(container.querySelector("img")).toBeNull();
    expect(loadBlob).not.toHaveBeenCalled();
    await fireEvent.click(container.querySelector("[data-binary-chip]")!);
    await tick();
    await tick();
    expect(loadBlob).toHaveBeenCalledWith("e1");
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:z");
  });
});
