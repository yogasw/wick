import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";
import {
  fetchStatus,
  install,
  start,
  setAutostart,
  setExternal,
  reqStreamURL,
  logStreamURL,
} from "../api.js";

// Mock HttpClient layer per the fe-module TDD Layer-1 contract: the api
// Effects carry no layer, so tests provide this instead of the real one.
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

// Capture layer: records the outgoing request so we can assert URL/method.
function captureLayer(body: unknown) {
  const ref: { req: HttpClientRequest.HttpClientRequest | null } = { req: null };
  const layer = Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) => {
      ref.req = req;
      return Effect.succeed(
        HttpClientResponse.fromWeb(
          req,
          new Response(JSON.stringify(body), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );
    }),
  );
  return { ref, layer };
}

describe("router9 api", () => {
  test("fetchStatus parses state + version", async () => {
    const s = await Effect.runPromise(
      fetchStatus("/tools/agents").pipe(
        Effect.provide(mockLayer(200, { installed: true, version: "1.2.3", running: true, state: "running" })),
      ),
    );
    expect(s.state).toBe("running");
    expect(s.version).toBe("1.2.3");
  });

  test("fetchStatus GETs the right endpoint", async () => {
    const { ref, layer } = captureLayer({ installed: false, version: "", running: false, state: "stopped" });
    await Effect.runPromise(fetchStatus("/tools/agents").pipe(Effect.provide(layer)));
    const r = ref.req as unknown as HttpClientRequest.HttpClientRequest;
    expect(r.url).toContain("/tools/agents/9router/status");
    expect(r.method).toBe("GET");
  });

  test("logStreamURL builds the SSE log endpoint", () => {
    expect(logStreamURL("/tools/agents")).toBe("/tools/agents/9router/logstream");
  });

  test("install POSTs and returns version", async () => {
    const { ref, layer } = captureLayer({ version: "9.9.9" });
    const r = await Effect.runPromise(install("/tools/agents").pipe(Effect.provide(layer)));
    expect(r.version).toBe("9.9.9");
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).method).toBe("POST");
  });

  test("start POSTs the start endpoint", async () => {
    const { ref, layer } = captureLayer({ status: "running" });
    await Effect.runPromise(start("/tools/agents").pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/9router/start");
  });

  test("setAutostart passes ?on=true", async () => {
    const { ref, layer } = captureLayer({ autostart: true });
    await Effect.runPromise(setAutostart("/tools/agents", true).pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/9router/autostart?on=true");
  });

  test("setAutostart passes ?on=false when disabling", async () => {
    const { ref, layer } = captureLayer({ autostart: false });
    await Effect.runPromise(setAutostart("/tools/agents", false).pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("on=false");
  });

  test("setExternal passes ?on=true", async () => {
    const { ref, layer } = captureLayer({ external: true });
    await Effect.runPromise(setExternal("/tools/agents", true).pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/9router/external?on=true");
  });

  test("fails with APIError on non-2xx", async () => {
    const err = await Effect.runPromise(
      fetchStatus("/tools/agents").pipe(Effect.flip, Effect.provide(mockLayer(500, { error: "boom" }))),
    );
    expect(err).toBeInstanceOf(APIError);
    expect(err.status).toBe(500);
  });

  test("reqStreamURL builds the SSE endpoint", () => {
    expect(reqStreamURL("/tools/agents")).toBe("/tools/agents/9router/reqstream");
  });
});
