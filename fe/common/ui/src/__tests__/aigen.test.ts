import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { AIGenRun, submitAIGen, type AIGenAPI, type AIGenJob, type AIGenState } from "../aigen.js";

function fakeAPI(sequence: AIGenJob<string>[]) {
  const gets = [...sequence];
  const api: AIGenAPI<string> = {
    submit: vi.fn(async () => ({ id: "gen_1", kind: "k", status: "queued", position: 2 }) as AIGenJob<string>),
    get: vi.fn(async () => gets.shift() ?? ({ id: "gen_1", kind: "k", status: "working" } as AIGenJob<string>)),
    cancel: vi.fn(async () => ({ id: "gen_1", kind: "k", status: "canceled" }) as AIGenJob<string>),
  };
  return api;
}

describe("AIGenRun", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  test("walks queued → working → done, polling the job", async () => {
    const api = fakeAPI([
      { id: "gen_1", kind: "k", status: "queued", position: 1 },
      { id: "gen_1", kind: "k", status: "working" },
      { id: "gen_1", kind: "k", status: "done", result: "hello" },
    ]);
    const seen: AIGenState<string>[] = [];
    const run = new AIGenRun<string>({ kind: "k", api, pollMs: 1000, onChange: (s) => seen.push(s) });
    await run.start({ text: "brief" });
    expect(api.submit).toHaveBeenCalledWith("k", { text: "brief" });
    expect(run.current).toMatchObject({ phase: "queued", position: 2, jobId: "gen_1" });

    await vi.advanceTimersByTimeAsync(1000);
    expect(run.current).toMatchObject({ phase: "queued", position: 1 });
    await vi.advanceTimersByTimeAsync(1000);
    expect(run.current.phase).toBe("working");
    await vi.advanceTimersByTimeAsync(1000);
    expect(run.current).toMatchObject({ phase: "done", result: "hello" });
    expect(api.get).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(5000);
    expect(api.get).toHaveBeenCalledTimes(3); // stops polling once done
    expect(seen.map((s) => s.phase)).toEqual(["queued", "queued", "queued", "working", "done"]);
  });

  test("a failed job surfaces its error", async () => {
    const api = fakeAPI([{ id: "gen_1", kind: "k", status: "failed", error: "no free agent slot" }]);
    const run = new AIGenRun<string>({ kind: "k", api, onChange: () => {} });
    await run.start({ text: "x" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(run.current).toMatchObject({ phase: "failed", error: "no free agent slot" });
  });

  test("a submit error fails without polling", async () => {
    const api = fakeAPI([]);
    vi.mocked(api.submit).mockRejectedValueOnce(new Error("you already have generate jobs waiting"));
    const run = new AIGenRun<string>({ kind: "k", api, onChange: () => {} });
    await run.start({ text: "x" });
    expect(run.current).toMatchObject({ phase: "failed", error: "you already have generate jobs waiting" });
    await vi.advanceTimersByTimeAsync(3000);
    expect(api.get).not.toHaveBeenCalled();
  });

  test("cancel calls the server, returns to idle and drops late polls", async () => {
    const api = fakeAPI([{ id: "gen_1", kind: "k", status: "done", result: "late" }]);
    const run = new AIGenRun<string>({ kind: "k", api, onChange: () => {} });
    await run.start({ text: "x" });
    await run.cancel();
    expect(api.cancel).toHaveBeenCalledWith("gen_1");
    expect(run.current.phase).toBe("idle");
    await vi.advanceTimersByTimeAsync(3000);
    expect(api.get).not.toHaveBeenCalled();
    expect(run.current.phase).toBe("idle");
  });

  test("dispose cancels an in-flight job", async () => {
    const api = fakeAPI([]);
    const run = new AIGenRun<string>({ kind: "k", api, onChange: () => {} });
    await run.start({ text: "x" });
    run.dispose();
    await vi.advanceTimersByTimeAsync(0);
    expect(api.cancel).toHaveBeenCalledWith("gen_1");
  });
});

describe("submitAIGen", () => {
  afterEach(() => vi.unstubAllGlobals());

  test("posts kind + input and surfaces the server error message", async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ error: "unknown generate kind" }), { status: 400 }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(submitAIGen("nope", { text: "x" })).rejects.toThrow("unknown generate kind");
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/ai-gen");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({ kind: "nope", input: { text: "x" } });
  });
});
