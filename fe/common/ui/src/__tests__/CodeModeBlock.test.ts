import { describe, it, expect, vi, afterEach } from "vitest";
import { render, cleanup, waitFor } from "@testing-library/svelte";
import TraceBody from "../trace/TraceBody.svelte";
import { classifyCall, classifyResult } from "../trace/classify.js";
import type { TraceContext, TraceFileStat } from "../trace/types.js";

afterEach(() => cleanup());

const TOOL = "mcp__wick__wick_run_code";
const CALLED = Date.parse("2026-01-02T03:04:05Z");
const SCRIPT = 'const r = await wick.call("conn:acme-1/list_rows", {});\nreturn r.length;';
const CALL = JSON.stringify({ script_path: "scripts/report.ts", timeout_ms: 120000, input: { day: "2026-01-02" } });

function ctx(st: TraceFileStat, over: Partial<TraceContext> = {}): TraceContext {
  return {
    calledAt: CALLED,
    statPath: vi.fn(async () => st),
    // jsdom's Blob has no text(); the browser's does.
    loadPath: vi.fn(async () => ({ text: async () => SCRIPT }) as Blob),
    ...over,
  };
}

function card(c?: TraceContext, raw = CALL) {
  return render(TraceBody, { raw, toolName: TOOL, call: true, ctx: c });
}

describe("code mode call card", () => {
  it("script_path: reads the file through the session endpoints and shows it", async () => {
    const c = ctx({ status: "present", rel: "scripts/report.ts", size: SCRIPT.length, mtime: CALLED - 60_000 });
    const { container } = card(c);
    await waitFor(() => expect(container.querySelector("[data-trace-kind=code]")?.textContent).toContain("list_rows"));
    expect(c.statPath).toHaveBeenCalledWith("scripts/report.ts");
    expect(c.loadPath).toHaveBeenCalledWith("scripts/report.ts");
    expect(container.querySelector("[data-path]")?.textContent).toBe("scripts/report.ts");
    expect(container.querySelector("[data-file-state]")?.textContent).toBe("✓ same as run");
    expect(container.querySelector("[data-script-title]")?.textContent).toBe("script · typescript · 2 lines");
    expect(container.querySelector("[data-gutter]")?.textContent).toBe("1\n2");
    // The input comes first, as its own titled part, then the script.
    const title = container.querySelector("[data-trace-part-title]")!;
    expect(title.textContent).toBe("input");
    expect(title.compareDocumentPosition(container.querySelector("[data-trace-kind=codemode]")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(container.textContent).toContain("2026-01-02");
  });

  it("changed after the run: says so over the current file", async () => {
    const { container } = card(ctx({ status: "present", rel: "scripts/report.ts", size: 10, mtime: CALLED + 120_000 }));
    await waitFor(() => expect(container.querySelector("[data-file-state]")?.getAttribute("data-file-state")).toBe("changed"));
    expect(container.querySelector("[data-script-note]")?.textContent).toContain("not the script that ran");
  });

  it("no access: never loads the file, says it can't open it", async () => {
    const c = ctx({ status: "unknown" });
    const { container } = card(c);
    await waitFor(() => expect(container.querySelector("[data-script-note]")?.textContent).toContain("Can't open this file"));
    expect(c.loadPath).not.toHaveBeenCalled();
    expect(container.querySelector("[data-trace-kind=code]")).toBeNull();
    expect(container.querySelector("[data-timeout]")?.textContent).toBe("timeout 2m");
  });

  it("deleted: says what ran can't be shown", async () => {
    const c = ctx({ status: "missing" });
    const { container } = card(c);
    await waitFor(() => expect(container.querySelector("[data-file-state]")?.getAttribute("data-file-state")).toBe("missing"));
    expect(c.loadPath).not.toHaveBeenCalled();
  });

  it("no file access wired: says so instead of showing the raw call", async () => {
    const { container } = card(undefined);
    await waitFor(() => expect(container.querySelector("[data-script-note]")?.textContent).toContain("can't be opened from here"));
  });

  it("an old stored mcp display is reclassified", async () => {
    const old = { kind: "mcp", lang: "typescript", connector: "code mode", op: "ts", summary: "code mode · ts · 0 tools" };
    const c = ctx({ status: "present", rel: "scripts/report.ts", size: SCRIPT.length, mtime: CALLED - 60_000 });
    const { container } = render(TraceBody, { display: old, raw: CALL, toolName: TOOL, call: true, ctx: c });
    await waitFor(() => expect(container.querySelector("[data-trace-kind=codemode]")).not.toBeNull());
  });

  it("inline code renders as code, no file read", () => {
    const d = classifyCall(TOOL, JSON.stringify({ code: SCRIPT }));
    expect(d.kind).toBe("codemode");
    expect(d.path).toBeUndefined();
    expect(d.body).toBe(SCRIPT);
  });
});

describe("code mode result card", () => {
  it("return value as body; error, calls and logs as titled parts", () => {
    const raw = JSON.stringify({
      ok: true, result: { rows: 2 }, logs: ["a", "b"],
      calls: [{ tool_id: "conn:acme-1/list_rows", ok: true, ms: 12 }, { tool_id: "conn:acme-1/get_row", ok: false, ms: 3, error: "denied" }],
      duration_ms: 40, run_id: "r1",
    });
    const d = classifyResult(TOOL, raw);
    expect(d.kind).toBe("json");
    expect(JSON.parse(d.body ?? "")).toEqual({ rows: 2 });
    expect(d.summary).toBe("ok · 2 calls · 40 ms");
    expect(d.parts?.map((p) => p.title)).toEqual(["calls", "logs"]);
    expect(d.parts?.[0].body).toBe("✓ conn:acme-1/list_rows · 12 ms\n✗ conn:acme-1/get_row · 3 ms · denied");
  });
});
