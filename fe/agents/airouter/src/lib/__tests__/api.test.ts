import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";
import {
  fetchRouters,
  fetchStatus,
  install,
  start,
  setAutostart,
  setExternal,
  reqStreamURL,
  logStreamURL,
  dashboardURL,
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

describe("airouter api", () => {
  test("fetchRouters lists the registered routers", async () => {
    const { ref, layer } = captureLayer({ routers: [{ id: "9router", name: "9router" }] });
    const r = await Effect.runPromise(fetchRouters("/tools/agents").pipe(Effect.provide(layer)));
    expect(r.routers[0].id).toBe("9router");
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/tools/agents/airouter/routers");
  });

  test("fetchStatus parses state + version", async () => {
    const s = await Effect.runPromise(
      fetchStatus("/tools/agents", "9router").pipe(
        Effect.provide(mockLayer(200, { installed: true, version: "1.2.3", running: true, state: "running" })),
      ),
    );
    expect(s.state).toBe("running");
    expect(s.version).toBe("1.2.3");
  });

  test("fetchStatus GETs the per-router endpoint", async () => {
    const { ref, layer } = captureLayer({ installed: false, version: "", running: false, state: "stopped" });
    await Effect.runPromise(fetchStatus("/tools/agents", "omniroute").pipe(Effect.provide(layer)));
    const r = ref.req as unknown as HttpClientRequest.HttpClientRequest;
    expect(r.url).toContain("/tools/agents/airouter/omniroute/status");
    expect(r.method).toBe("GET");
  });

  test("logStreamURL builds the per-router SSE log endpoint", () => {
    expect(logStreamURL("/tools/agents", "9router")).toBe("/tools/agents/airouter/9router/logstream");
  });

  test("reqStreamURL builds the per-router SSE endpoint", () => {
    expect(reqStreamURL("/tools/agents", "omniroute")).toBe("/tools/agents/airouter/omniroute/reqstream");
  });

  test("dashboardURL points at the wick-root proxy mount", () => {
    expect(dashboardURL("9router")).toBe("/airouter/9router/");
  });

  test("install POSTs and returns version", async () => {
    const { ref, layer } = captureLayer({ version: "9.9.9" });
    const r = await Effect.runPromise(install("/tools/agents", "9router").pipe(Effect.provide(layer)));
    expect(r.version).toBe("9.9.9");
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).method).toBe("POST");
  });

  test("start POSTs the per-router start endpoint", async () => {
    const { ref, layer } = captureLayer({ status: "running" });
    await Effect.runPromise(start("/tools/agents", "9router").pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/airouter/9router/start");
  });

  test("setAutostart passes ?on=true", async () => {
    const { ref, layer } = captureLayer({ autostart: true });
    await Effect.runPromise(setAutostart("/tools/agents", "9router", true).pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/airouter/9router/autostart?on=true");
  });

  test("setAutostart passes ?on=false when disabling", async () => {
    const { ref, layer } = captureLayer({ autostart: false });
    await Effect.runPromise(setAutostart("/tools/agents", "9router", false).pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("on=false");
  });

  test("setExternal passes ?on=true", async () => {
    const { ref, layer } = captureLayer({ external: true });
    await Effect.runPromise(setExternal("/tools/agents", "omniroute", true).pipe(Effect.provide(layer)));
    expect((ref.req as unknown as HttpClientRequest.HttpClientRequest).url).toContain("/airouter/omniroute/external?on=true");
  });

  test("fails with APIError on non-2xx", async () => {
    const err = await Effect.runPromise(
      fetchStatus("/tools/agents", "9router").pipe(Effect.flip, Effect.provide(mockLayer(500, { error: "boom" }))),
    );
    expect(err).toBeInstanceOf(APIError);
    expect(err.status).toBe(500);
  });
});
