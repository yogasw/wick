import { describe, it, expect, vi, beforeEach } from "vitest";
import { getProviderOptions, getPresetOptions, getProjectOptions, createSession } from "../api/options.js";
import { Effect } from "effect";
import { WickClientLayer } from "@wick-fe/common-api";

const mockFetch = vi.fn();
globalThis.fetch = mockFetch;

function makeOkResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    redirected: false,
    url: "",
    json: () => Promise.resolve(body),
    text: () => Promise.resolve(JSON.stringify(body)),
    headers: new Headers({ "content-type": "application/json" }),
  } as unknown as Response;
}

function makeRedirectResponse(url: string): Response {
  return {
    ok: true,
    status: 200,
    redirected: true,
    url,
    json: () => Promise.resolve({}),
    text: () => Promise.resolve(""),
    headers: new Headers(),
  } as unknown as Response;
}

function makeErrorResponse(status: number, body: string): Response {
  return {
    ok: false,
    status,
    redirected: false,
    url: "",
    json: () => Promise.reject(new Error("not json")),
    text: () => Promise.resolve(body),
    headers: new Headers(),
  } as unknown as Response;
}

beforeEach(() => {
  mockFetch.mockReset();
});

describe("getProviderOptions", () => {
  it("returns parsed providers with a normalized usesAIRouter flag", async () => {
    const data = [{ type: "claude", name: "Claude", version: "3" }];
    mockFetch.mockResolvedValueOnce(makeOkResponse(data));
    const result = await Effect.runPromise(
      getProviderOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result).toEqual([{ ...data[0], usesAIRouter: false }]);
  });

  it("maps the snake_case uses_airouter flag from the backend", async () => {
    mockFetch.mockResolvedValueOnce(
      makeOkResponse([{ type: "codex", name: "Codex", version: "1", uses_airouter: true }]),
    );
    const result = await Effect.runPromise(
      getProviderOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result[0].usesAIRouter).toBe(true);
  });

  it("normalizes null to empty array", async () => {
    mockFetch.mockResolvedValueOnce(makeOkResponse(null));
    const result = await Effect.runPromise(
      getProviderOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result).toEqual([]);
  });
});

describe("getPresetOptions", () => {
  it("returns parsed presets", async () => {
    const data = [{ name: "default" }, { name: "coding" }];
    mockFetch.mockResolvedValueOnce(makeOkResponse(data));
    const result = await Effect.runPromise(
      getPresetOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result).toEqual(data);
  });

  it("normalizes null to empty array", async () => {
    mockFetch.mockResolvedValueOnce(makeOkResponse(null));
    const result = await Effect.runPromise(
      getPresetOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result).toEqual([]);
  });
});

describe("getProjectOptions", () => {
  it("returns parsed projects with managed defaulted", async () => {
    const data = [{ id: "p1", name: "My Project", path: "/tmp/p1" }];
    mockFetch.mockResolvedValueOnce(makeOkResponse(data));
    const result = await Effect.runPromise(
      getProjectOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result[0].managed).toBe(false);
    expect(result[0].id).toBe("p1");
  });

  it("normalizes null to empty array", async () => {
    mockFetch.mockResolvedValueOnce(makeOkResponse(null));
    const result = await Effect.runPromise(
      getProjectOptions("/tools/agents").pipe(Effect.provide(WickClientLayer)),
    );
    expect(result).toEqual([]);
  });
});

describe("createSession", () => {
  it("builds FormData with all fields and returns redirect url", async () => {
    const redirectUrl = "/tools/agents/sessions/abc-123";
    mockFetch.mockResolvedValueOnce(makeRedirectResponse(redirectUrl));

    const url = await createSession(
      "/tools/agents",
      "Hello world",
      [],
      "claude",
      "default",
      "proj-1",
    );

    expect(url).toBe(redirectUrl);
    expect(mockFetch).toHaveBeenCalledOnce();
    const [fetchUrl, fetchInit] = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(fetchUrl).toBe("/tools/agents/");
    expect(fetchInit.method).toBe("POST");
    expect(fetchInit.credentials).toBe("same-origin");
    const fd = fetchInit.body as FormData;
    expect(fd.get("message")).toBe("Hello world");
    expect(fd.get("provider")).toBe("claude");
    expect(fd.get("preset")).toBe("default");
    expect(fd.get("project_id")).toBe("proj-1");
  });

  it("throws on non-ok non-redirect response", async () => {
    mockFetch.mockResolvedValueOnce(makeErrorResponse(500, "internal error"));
    await expect(
      createSession("/tools/agents", "hello", [], "claude", "default", ""),
    ).rejects.toThrow("internal error");
  });

  it("appends files to FormData", async () => {
    const redirectUrl = "/tools/agents/sessions/xyz";
    mockFetch.mockResolvedValueOnce(makeRedirectResponse(redirectUrl));
    const file = new File(["content"], "test.txt", { type: "text/plain" });

    await createSession("/tools/agents", "msg", [file], "claude", "default", "");

    const fd = mockFetch.mock.calls[0][1].body as FormData;
    const appended = fd.getAll("files");
    expect(appended).toHaveLength(1);
    expect((appended[0] as File).name).toBe("test.txt");
  });
});
