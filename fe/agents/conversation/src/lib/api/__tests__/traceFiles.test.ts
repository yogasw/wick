import { describe, it, expect, vi } from "vitest";
import { makeTraceFiles } from "../files.js";

function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

describe("makeTraceFiles", () => {
  it("batches stats asked in the same tick and caches them per path", async () => {
    const f = vi.fn(async (url: string) => {
      const paths = new URL(url, "http://x").searchParams.getAll("path");
      return jsonRes({ files: paths.map((p) => ({ path: p, status: p.endsWith("gone.png") ? "missing" : "present", rel: "a.png", size: 3 })) });
    });
    const tf = makeTraceFiles("/tools/agents", "s1", f as unknown as typeof fetch);
    const [a, b] = await Promise.all([tf.stat("/p/a.png"), tf.stat("/p/gone.png")]);
    expect(f).toHaveBeenCalledTimes(1);
    expect(f.mock.calls[0][0]).toBe("/tools/agents/sessions/s1/files/stat?path=%2Fp%2Fa.png&path=%2Fp%2Fgone.png");
    expect(a.status).toBe("present");
    expect(b.status).toBe("missing");
    await tf.stat("/p/a.png");
    expect(f).toHaveBeenCalledTimes(1);
  });

  it("a failed check reads as unknown, never as deleted", async () => {
    const tf = makeTraceFiles("/b", "s1", (async () => jsonRes({ error: "x" }, 500)) as unknown as typeof fetch);
    expect((await tf.stat("/p/a.png")).status).toBe("unknown");
  });

  it("load names the status so a 404 can read as deleted", async () => {
    const tf = makeTraceFiles("/b", "s1", (async () => new Response("", { status: 404 })) as unknown as typeof fetch);
    await expect(tf.load("a.png")).rejects.toThrow(/: 404$/);
  });
});
