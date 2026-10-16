import { describe, test, expect, beforeEach, afterEach, vi } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { getProjectOptions, invalidateProjectOptions, pinProject, PROJECT_OPTIONS_TTL_MS } from "../options.js";

/* A server that counts requests per URL and answers each with the current
   `rows`, optionally held until release() so a request stays in flight. */
function server() {
  const calls: string[] = [];
  let rows: unknown[] = [{ id: "p1", name: "One", path: "" }];
  let held: (() => void)[] | null = null;
  const layer = Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) =>
      Effect.promise(async () => {
        calls.push(req.url);
        if (held) await new Promise<void>((r) => held!.push(r));
        const body = req.url.endsWith("/pin") ? { status: "ok", pinned: true, project_id: "p1" } : rows;
        return HttpClientResponse.fromWeb(
          req,
          new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }),
        );
      }),
    ),
  );
  return {
    calls,
    setRows: (r: unknown[]) => { rows = r; },
    hold: () => { held = []; },
    release: () => { const h = held ?? []; held = null; h.forEach((r) => r()); },
    get: (opts?: Parameters<typeof getProjectOptions>[1]) =>
      Effect.runPromise(getProjectOptions("/tools/agents", opts).pipe(Effect.provide(layer))),
    pin: () => Effect.runPromise(pinProject("/tools/agents", "p1").pipe(Effect.provide(layer))),
  };
}

const optionCalls = (calls: string[]) => calls.filter((u) => u.includes("/projects/options"));

describe("project options cache", () => {
  beforeEach(() => invalidateProjectOptions());
  afterEach(() => vi.useRealTimers());

  test("concurrent callers share one in-flight request", async () => {
    const s = server();
    s.hold();
    const all = Promise.all([s.get(), s.get(), s.get()]);
    await new Promise((r) => setTimeout(r, 0));
    s.release();
    const [a, b, c] = await all;
    expect(optionCalls(s.calls)).toHaveLength(1);
    expect(a.map((p) => p.id)).toEqual(["p1"]);
    // Each caller gets its own objects.
    expect(a[0]).not.toBe(b[0]);
    expect(c).toEqual(a);
  });

  test("a fresh answer is served from cache; another query is its own key", async () => {
    const s = server();
    await s.get();
    await s.get();
    expect(optionCalls(s.calls)).toHaveLength(1);
    await s.get({ hideTeam: true });
    expect(optionCalls(s.calls)).toHaveLength(2);
    expect(s.calls[1]).toContain("hide_team=1");
  });

  test("a stale answer comes back at once and is refreshed in the background", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const s = server();
    await s.get();
    s.setRows([{ id: "p2", name: "Two", path: "" }]);
    vi.setSystemTime(Date.now() + PROJECT_OPTIONS_TTL_MS + 1);
    const stale = await s.get();
    expect(stale.map((p) => p.id)).toEqual(["p1"]);
    await vi.waitFor(() => expect(optionCalls(s.calls)).toHaveLength(2));
    await vi.waitFor(async () => expect((await s.get()).map((p) => p.id)).toEqual(["p2"]));
    expect(optionCalls(s.calls)).toHaveLength(2);
  });

  test("pinning a project drops the cache", async () => {
    const s = server();
    await s.get();
    await s.pin();
    s.setRows([{ id: "p1", name: "One", path: "", pinned: true }]);
    const after = await s.get();
    expect(optionCalls(s.calls)).toHaveLength(2);
    expect(after[0].pinned).toBe(true);
  });

  test("an answer that lands after invalidation is not cached", async () => {
    const s = server();
    s.hold();
    const first = s.get();
    await new Promise((r) => setTimeout(r, 0));
    invalidateProjectOptions();
    s.release();
    await first;
    await s.get();
    expect(optionCalls(s.calls)).toHaveLength(2);
  });

  test("a failed request is not cached", async () => {
    let fail = true;
    const layer = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) =>
        Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify(fail ? { error: "boom" } : []), {
              status: fail ? 500 : 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        ),
      ),
    );
    const get = () => Effect.runPromise(getProjectOptions("/tools/agents").pipe(Effect.provide(layer)));
    await expect(get()).rejects.toBeTruthy();
    fail = false;
    await expect(get()).resolves.toEqual([]);
  });
});
