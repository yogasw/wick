import { describe, it, expect, vi, afterEach } from "vitest";
import { render, fireEvent, cleanup } from "@testing-library/svelte";
import { tick } from "svelte";
import TraceBody from "../trace/TraceBody.svelte";
import { registerTraceRenderer } from "../trace/registry.js";
import TextBlock from "../trace/blocks/TextBlock.svelte";
import type { TraceDisplay } from "../trace/types.js";

afterEach(() => cleanup());

const PNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";

describe("TraceBody kinds", () => {
  const cases: [string, TraceDisplay, string][] = [
    ["markdown", { kind: "markdown", body: "# Title\n\n- **a**" }, "[data-trace-kind=markdown] strong"],
    ["json", { kind: "json", body: '{"a":1}' }, "[data-trace-kind=json]"],
    ["code", { kind: "code", body: "package main", lang: "go" }, "[data-trace-kind=code][data-lang=go]"],
    ["diff", { kind: "diff", body: "--- a\n+++ b\n@@ -1 +1 @@\n-x\n+y" }, "[data-trace-kind=diff]"],
    ["text", { kind: "text", body: "hello" }, "[data-trace-kind=text]"],
    ["error", { kind: "error", body: "boom" }, "[data-trace-kind=error]"],
    ["terminal", { kind: "terminal", body: "out", exit_code: 1 }, "[data-exit-code]"],
    ["command", { kind: "command", command: "git status", cwd: "/repo" }, "[data-cwd]"],
    ["file", { kind: "file", path: "/r/a.go", body: "x", lang: "go" }, "[data-path]"],
    ["search", { kind: "search", pattern: "TODO", path: "internal" }, "[data-trace-kind=search]"],
    ["mcp", { kind: "mcp", connector: "slack", op: "send", body: '{"a":1}' }, "[data-connector]"],
    ["image", { kind: "image", mime: "image/png", original_bytes: 524288, name: "shot.png", blob_ref: "e1" }, "[data-binary-chip]"],
  ];
  for (const [name, d, sel] of cases) {
    it(name, () => {
      const { container } = render(TraceBody, { display: d });
      expect(container.querySelector(sel), sel).not.toBeNull();
    });
  }

  it("terminal exit badge reads the exit code", () => {
    const { getByText } = render(TraceBody, { display: { kind: "terminal", body: "x", exit_code: 1 } });
    expect(getByText("exit 1")).toBeTruthy();
  });

  it("unknown kind falls back to text", () => {
    const { container } = render(TraceBody, { display: { kind: "mermaid-v9", body: "graph TD" } });
    expect(container.querySelector("[data-trace-kind=text]")?.textContent).toBe("graph TD");
  });

  it("registered kind renders through the registry", () => {
    registerTraceRenderer("custom-x", TextBlock);
    const { container } = render(TraceBody, { display: { kind: "custom-x", body: "z" } });
    expect(container.querySelector("[data-trace-kind=text]")).not.toBeNull();
  });

  it("classifies when the event has no display", () => {
    const { container } = render(TraceBody, { raw: JSON.stringify("# P1\n\nbody"), toolName: "Agent" });
    expect(container.querySelector("[data-trace-body]")?.getAttribute("data-kind")).toBe("markdown");
    expect(container.querySelector("[data-trace-kind=markdown]")?.textContent).toContain("P1");
  });
});

describe("TraceBody frame", () => {
  it("Raw toggle shows the untouched event text", async () => {
    const raw = JSON.stringify("# P1\n\nbody");
    const { container } = render(TraceBody, { raw, toolName: "Agent" });
    expect(container.querySelector("[data-trace-raw-text]")).toBeNull();
    await fireEvent.click(container.querySelector("[data-trace-raw]")!);
    expect(container.querySelector("[data-trace-raw-text]")?.textContent).toBe(raw);
    expect(container.querySelector("[data-trace-kind=markdown]")).toBeNull();
  });

  it("caps height and offers Expand when content overflows", async () => {
    const spy = vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(2000);
    const { container } = render(TraceBody, { display: { kind: "text", body: "x\n".repeat(500) } });
    await tick();
    const box = container.querySelector("[data-trace-scrollbox] > div") as HTMLElement;
    expect(box.style.maxHeight).toBe("320px");
    const btn = container.querySelector("[data-trace-expand]") as HTMLButtonElement;
    expect(btn.textContent).toBe("Expand");
    await fireEvent.click(btn);
    expect(box.style.maxHeight).toBe("");
    expect(btn.textContent).toBe("Collapse");
    spy.mockRestore();
  });

  it("truncated json renders as json code plus the banner", () => {
    const body = '{"rows":[{"a":1},{"a":';
    const { container } = render(TraceBody, { display: { kind: "json", lang: "json", truncated: true, original_bytes: 51234 }, raw: body });
    expect(container.querySelector("[data-trace-kind=code][data-lang=json]")?.textContent).toContain('"rows"');
    expect(container.querySelector("[data-trace-truncated]")?.textContent).toBe(
      "Trace truncated — showing 1 of 50 KB (the agent saw the full output)",
    );
  });

  it("payload-level truncated flag shows the banner too", () => {
    const { container } = render(TraceBody, { raw: "plain output", truncated: true });
    expect(container.querySelector("[data-trace-truncated]")).not.toBeNull();
  });
});

describe("BinaryChip", () => {
  it("shows a chip without decoding or <img> until clicked (live data)", async () => {
    const atob_ = vi.spyOn(globalThis, "atob");
    const createUrl = vi.fn(() => "blob:x");
    globalThis.URL.createObjectURL = createUrl;
    globalThis.URL.revokeObjectURL = vi.fn();
    const { container } = render(TraceBody, { raw: "data:image/png;base64," + PNG });
    const chip = container.querySelector("[data-binary-chip]") as HTMLButtonElement;
    expect(chip.textContent).toContain("image.png · PNG · 70 B");
    expect(container.querySelector("img")).toBeNull();
    // Classifying peeks at the first 32 chars only — never the whole payload.
    expect(atob_.mock.calls.every(([s]) => (s as string).length <= 32)).toBe(true);
    atob_.mockClear();
    await fireEvent.click(chip);
    await tick();
    expect(atob_).toHaveBeenCalledTimes(1);
    expect(createUrl).toHaveBeenCalledTimes(1);
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:x");
    expect(container.querySelector("[data-binary-download]")?.getAttribute("download")).toBe("image.png");
    atob_.mockRestore();
  });

  it("fetches a stored blob by ref only on click", async () => {
    globalThis.URL.createObjectURL = vi.fn(() => "blob:y");
    globalThis.URL.revokeObjectURL = vi.fn();
    const loadBlob = vi.fn(async () => new Blob(["x"], { type: "image/png" }));
    const onOpenMedia = vi.fn();
    const d: TraceDisplay = { kind: "image", mime: "image/png", original_bytes: 524288, name: "shot.png", blob_ref: "e1" };
    const { container } = render(TraceBody, { display: d, ctx: { loadBlob, onOpenMedia } });
    expect(container.querySelector("[data-binary-chip]")?.textContent).toContain("shot.png · PNG · 512 KB");
    expect(loadBlob).not.toHaveBeenCalled();
    await fireEvent.click(container.querySelector("[data-binary-chip]")!);
    await tick();
    await tick();
    expect(loadBlob).toHaveBeenCalledWith("e1");
    await fireEvent.click(container.querySelector("img")!.closest("button")!);
    expect(onOpenMedia).toHaveBeenCalledWith({ url: "blob:y", name: "shot.png", kind: "image", mime: "image/png" });
  });

  it("live backend display without blob_ref decodes from the raw base64", async () => {
    globalThis.URL.createObjectURL = vi.fn(() => "blob:live");
    globalThis.URL.revokeObjectURL = vi.fn();
    const raw = JSON.stringify([{ type: "text", text: "shot" }, { type: "image", data: PNG, mimeType: "image/png" }]);
    const display: TraceDisplay = { kind: "text", body: "shot", parts: [{ kind: "image", mime: "image/png", original_bytes: 70, name: "image.png" }] };
    const { container } = render(TraceBody, { display, raw });
    const chip = container.querySelector("[data-binary-chip]") as HTMLButtonElement;
    expect(chip.disabled).toBe(false);
    expect(container.querySelector("img")).toBeNull();
    await fireEvent.click(chip);
    await tick();
    expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:live");
  });

  it("too_large is info only", () => {
    const { container } = render(TraceBody, {
      display: { kind: "image", mime: "image/png", original_bytes: 14 << 20, too_large: true },
      ctx: { loadBlob: vi.fn() },
    });
    const chip = container.querySelector("[data-binary-chip]") as HTMLButtonElement;
    expect(chip.disabled).toBe(true);
    expect(chip.textContent).toContain("too large to keep in trace");
  });

  it("Raw for a binary is a short preview, not the base64", async () => {
    const raw = "data:image/png;base64," + "iVBORw0KGgo" + "A".repeat(5000);
    const { container } = render(TraceBody, { raw });
    await fireEvent.click(container.querySelector("[data-trace-raw]")!);
    const t = container.querySelector("[data-trace-raw-text]")!.textContent!;
    expect(t.length).toBeLessThan(260);
    expect(t).toMatch(/… 5 KB$/);
  });

  it("mixed result: text body plus image part chip", () => {
    const raw = JSON.stringify([{ type: "text", text: "Took screenshot" }, { type: "image", data: PNG, mimeType: "image/png" }]);
    const { container } = render(TraceBody, { raw, toolName: "mcp__playwright__browser_take_screenshot" });
    expect(container.querySelector("[data-trace-kind=text]")?.textContent).toBe("Took screenshot");
    expect(container.querySelector("[data-binary-chip]")).not.toBeNull();
    expect(container.querySelector("img")).toBeNull();
  });
});
