import { describe, expect, it, vi } from "vitest";
import { loadAnalytics } from "$lib/stream";

/** Builds a Response whose body streams the given chunks verbatim, so a
 *  test can split a line anywhere a real network would. */
function streaming(chunks: string[]): Response {
  const enc = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const c of chunks) controller.enqueue(enc.encode(c));
      controller.close();
    },
  });
  return new Response(body, { status: 200, headers: { "Content-Type": "application/x-ndjson" } });
}

const RESULT = { generated_at: "2026-09-16T00:00:00Z", sessions: 4 };

describe("loadAnalytics", () => {
  it("reports progress on the way and returns the final payload", async () => {
    const fetchImpl = vi.fn(async () =>
      streaming([
        '{"type":"progress","done":0,"total":4}\n',
        '{"type":"progress","done":2,"total":4}\n',
        `{"type":"result","data":${JSON.stringify(RESULT)}}\n`,
      ]),
    ) as unknown as typeof fetch;

    const seen: Array<[number, number]> = [];
    const data = await loadAnalytics("/admin/analytics/users.json", {
      fetchImpl,
      onProgress: (done, total) => seen.push([done, total]),
    });

    expect(seen).toEqual([
      [0, 4],
      [2, 4],
    ]);
    expect(data.sessions).toBe(4);
  });

  // A chunk boundary lands wherever the network puts it, including in the
  // middle of a number. Parsing per chunk instead of per line is the bug
  // this pins.
  it("survives a line split across chunks", async () => {
    const line = `{"type":"result","data":${JSON.stringify(RESULT)}}\n`;
    const cut = Math.floor(line.length / 2);
    const fetchImpl = vi.fn(async () =>
      streaming(['{"type":"progr', 'ess","done":1,"total":2}\n', line.slice(0, cut), line.slice(cut)]),
    ) as unknown as typeof fetch;

    const seen: Array<[number, number]> = [];
    const data = await loadAnalytics("/x", { fetchImpl, onProgress: (d, t) => seen.push([d, t]) });
    expect(seen).toEqual([[1, 2]]);
    expect(data.sessions).toBe(4);
  });

  it("asks for the stream, and passes the window through", async () => {
    const fetchImpl = vi.fn(async () =>
      streaming([`{"type":"result","data":${JSON.stringify(RESULT)}}\n`]),
    ) as unknown as typeof fetch;
    await loadAnalytics("/admin/analytics/users.json", { fetchImpl, days: 90 });
    const url = new URL((fetchImpl as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0] as string);
    expect(url.searchParams.get("stream")).toBe("1");
    expect(url.searchParams.get("days")).toBe("90");
  });

  it("surfaces a server-side failure instead of hanging on it", async () => {
    const fetchImpl = vi.fn(async () =>
      streaming(['{"type":"error","error":"database is gone"}\n']),
    ) as unknown as typeof fetch;
    await expect(loadAnalytics("/x", { fetchImpl })).rejects.toThrow("database is gone");
  });

  // A stream that ends without its payload must not resolve with a
  // half-built page — the caller would render zeros as though they were
  // the answer.
  it("refuses a stream that ends without the data", async () => {
    const fetchImpl = vi.fn(async () =>
      streaming(['{"type":"progress","done":1,"total":9}\n']),
    ) as unknown as typeof fetch;
    await expect(loadAnalytics("/x", { fetchImpl })).rejects.toThrow(/without sending the data/);
  });

  it("reports an HTTP failure with its status", async () => {
    const fetchImpl = vi.fn(async () => new Response("nope", { status: 503, statusText: "Service Unavailable" })) as unknown as typeof fetch;
    await expect(loadAnalytics("/x", { fetchImpl })).rejects.toThrow(/503/);
  });

  // Some proxies strip the body stream. Progress is a nicety; the page
  // loading is not, so it falls back to the plain JSON endpoint.
  it("falls back to a plain fetch when there is no body to stream", async () => {
    const calls: string[] = [];
    const fetchImpl = vi.fn(async (url: string) => {
      calls.push(url);
      if (calls.length === 1) {
        const res = new Response(null, { status: 200 });
        Object.defineProperty(res, "body", { value: null });
        return res;
      }
      return new Response(JSON.stringify(RESULT), { status: 200 });
    }) as unknown as typeof fetch;

    const data = await loadAnalytics("/admin/analytics/users.json", { fetchImpl });
    expect(data.sessions).toBe(4);
    expect(new URL(calls[1]).searchParams.has("stream")).toBe(false);
  });
});

describe("window selection", () => {
  it("passes 'all' through as a value the server understands", async () => {
    const fetchImpl = vi.fn(async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(c) {
            c.enqueue(new TextEncoder().encode(`{"type":"result","data":{"sessions":1}}\n`));
            c.close();
          },
        }),
        { status: 200 },
      ),
    ) as unknown as typeof fetch;

    await loadAnalytics("/admin/analytics/users.json", { fetchImpl, days: "all" });
    const url = new URL((fetchImpl as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0] as string);
    expect(url.searchParams.get("days")).toBe("all");
  });
});

describe("a connection that goes quiet", () => {
  /** A body that emits `head`, then never says anything again. */
  function stalling(head: string[]): Response {
    const enc = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const c of head) controller.enqueue(enc.encode(c));
        // and then: nothing. No more chunks, no close.
      },
    });
    return new Response(body, { status: 200, headers: { "Content-Type": "application/x-ndjson" } });
  }

  // The bug this pins: the page sat on "Reading conversations… 2311 / 2311"
  // forever because a silent socket looks exactly like a busy server.
  it("retries once instead of waiting forever", async () => {
    let call = 0;
    const fetchImpl = vi.fn(async () => {
      call++;
      if (call === 1) return stalling(['{"type":"progress","done":2311,"total":2311}\n']);
      return streaming([`{"type":"result","data":${JSON.stringify(RESULT)}}\n`]);
    }) as unknown as typeof fetch;

    const data = await loadAnalytics("/x", { fetchImpl, stallMs: 20 });
    expect(call).toBe(2);
    expect(data.sessions).toBe(4);
  });

  it("gives up with a readable error when the retry stalls too", async () => {
    const fetchImpl = vi.fn(async () => stalling(['{"type":"progress","done":1,"total":9}\n'])) as unknown as typeof fetch;
    await expect(loadAnalytics("/x", { fetchImpl, stallMs: 20 })).rejects.toThrow(/went quiet/);
  });

  it("does not fire on a stream that is merely slow", async () => {
    const enc = new TextEncoder();
    const fetchImpl = vi.fn(async () => {
      const body = new ReadableStream<Uint8Array>({
        async start(controller) {
          controller.enqueue(enc.encode('{"type":"progress","done":1,"total":2}\n'));
          await new Promise((r) => setTimeout(r, 30));
          controller.enqueue(enc.encode(`{"type":"result","data":${JSON.stringify(RESULT)}}\n`));
          controller.close();
        },
      });
      return new Response(body, { status: 200 });
    }) as unknown as typeof fetch;

    const data = await loadAnalytics("/x", { fetchImpl, stallMs: 300 });
    expect(data.sessions).toBe(4);
  });
});
