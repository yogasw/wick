import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { getProviderOptions, getProjectOptions, switchProvider, moveProject } from "../options.js";
import type { ProviderOption, ProjectOption } from "../../types/agents.js";

/* ProviderOption and ProjectOption are used as type annotations in fixture constants below */

const mockLayer = (status: number, body: unknown) =>
  Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) =>
      Effect.succeed(
        HttpClientResponse.fromWeb(
          req,
          new Response(JSON.stringify(body), {
            status,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      ),
    ),
  );

const PROVIDER: ProviderOption = { type: "anthropic", name: "Claude Sonnet", version: "claude-sonnet-4" };
const PROJECT: ProjectOption = { id: "proj-1", name: "My Project", path: "/home/user/project", managed: false };

describe("getProviderOptions", () => {
  test("parses provider options array from response", async () => {
    const result = await Effect.runPromise(
      getProviderOptions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, [PROVIDER])),
      ),
    );
    expect(result).toHaveLength(1);
    expect(result[0].type).toBe("anthropic");
    expect(result[0].name).toBe("Claude Sonnet");
    expect(result[0].version).toBe("claude-sonnet-4");
  });

  test("normalizes null response to empty array", async () => {
    const result = await Effect.runPromise(
      getProviderOptions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, null)),
      ),
    );
    expect(result).toEqual([]);
  });

  test("returns empty array for empty response", async () => {
    const result = await Effect.runPromise(
      getProviderOptions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, [])),
      ),
    );
    expect(result).toHaveLength(0);
  });
});

describe("getProjectOptions", () => {
  test("parses project options array from response", async () => {
    const result = await Effect.runPromise(
      getProjectOptions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, [PROJECT])),
      ),
    );
    expect(result).toHaveLength(1);
    expect(result[0].id).toBe("proj-1");
    expect(result[0].name).toBe("My Project");
    expect(result[0].path).toBe("/home/user/project");
    expect(result[0].managed).toBe(false);
  });

  test("normalizes managed field to false when absent", async () => {
    const noManaged = { id: "proj-2", name: "Legacy", path: "/some/path" };
    const result = await Effect.runPromise(
      getProjectOptions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, [noManaged])),
      ),
    );
    expect(result[0].managed).toBe(false);
  });

  test("normalizes null response to empty array", async () => {
    const result = await Effect.runPromise(
      getProjectOptions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, null)),
      ),
    );
    expect(result).toEqual([]);
  });
});

describe("switchProvider", () => {
  test("posts to the correct session provider endpoint", async () => {
    let capturedReq: HttpClientRequest.HttpClientRequest | null = null;
    const captureLayer = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        capturedReq = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify({ status: "ok", provider: "anthropic" }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        );
      }),
    );

    const result = await Effect.runPromise(
      switchProvider("/tools/agents", "sess-1", "anthropic").pipe(
        Effect.provide(captureLayer),
      ),
    );
    const r = capturedReq as unknown as HttpClientRequest.HttpClientRequest;
    expect(result.status).toBe("ok");
    expect(result.provider).toBe("anthropic");
    expect(r.url).toContain("/sessions/sess-1/provider");
    expect(r.method).toBe("POST");
  });

  test("returns redirect url when provider switch creates new session", async () => {
    const result = await Effect.runPromise(
      switchProvider("/tools/agents", "sess-1", "openai").pipe(
        Effect.provide(
          mockLayer(200, { status: "redirect", redirect: "/sessions/sess-2" }),
        ),
      ),
    );
    expect(result.redirect).toBe("/sessions/sess-2");
  });
});

describe("moveProject", () => {
  test("posts to the correct session project endpoint", async () => {
    let capturedReq: HttpClientRequest.HttpClientRequest | null = null;
    const captureLayer = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        capturedReq = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify({ status: "ok", project_id: "proj-1" }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        );
      }),
    );

    const result = await Effect.runPromise(
      moveProject("/tools/agents", "sess-1", "proj-1").pipe(
        Effect.provide(captureLayer),
      ),
    );
    const r = capturedReq as unknown as HttpClientRequest.HttpClientRequest;
    expect((result as Record<string, unknown>).status).toBe("ok");
    expect(r.url).toContain("/sessions/sess-1/project");
    expect(r.method).toBe("POST");
  });
});
