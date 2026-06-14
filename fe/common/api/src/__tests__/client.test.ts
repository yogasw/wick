import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import {
  apiGetE,
  apiPostE,
  apiDeleteE,
  apiGet,
  runPromiseUnwrapped,
  APIError,
} from "../client.js";

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

describe("apiGetE", () => {
  test("resolves to parsed JSON on 2xx", async () => {
    const result = await Effect.runPromise(
      apiGetE<{ id: number }>("/api/test").pipe(Effect.provide(mockLayer(200, { id: 42 }))),
    );
    expect(result).toEqual({ id: 42 });
  });

  test("fails with APIError on 4xx", async () => {
    const err = await Effect.runPromise(
      apiGetE("/api/test").pipe(Effect.flip, Effect.provide(mockLayer(404, { error: "not found" }))),
    );
    expect(err).toBeInstanceOf(APIError);
  });

  test("APIError carries status and extracted detail", async () => {
    const err = await Effect.runPromise(
      apiGetE("/api/test").pipe(Effect.flip, Effect.provide(mockLayer(403, { error: "forbidden" }))),
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(403);
    expect((err as APIError).detail).toBe("forbidden");
  });
});

describe("apiPostE", () => {
  test("sends body and resolves JSON", async () => {
    const result = await Effect.runPromise(
      apiPostE<{ ok: boolean }>("/api/test", { name: "x" }).pipe(Effect.provide(mockLayer(200, { ok: true }))),
    );
    expect(result.ok).toBe(true);
  });
});

describe("apiDeleteE", () => {
  test("resolves JSON on 2xx", async () => {
    const result = await Effect.runPromise(
      apiDeleteE<{ deleted: boolean }>("/api/test").pipe(Effect.provide(mockLayer(200, { deleted: true }))),
    );
    expect(result.deleted).toBe(true);
  });
});

describe("Promise-compat layer", () => {
  test("runPromiseUnwrapped rejects with a real APIError (instanceof works for callers)", async () => {
    await expect(
      runPromiseUnwrapped(
        apiGetE("/api/test").pipe(Effect.provide(mockLayer(404, { error: "nope" }))),
      ),
    ).rejects.toBeInstanceOf(APIError);
  });

  test("runPromiseUnwrapped resolves value on success", async () => {
    const result = await runPromiseUnwrapped(
      apiGetE<{ id: number }>("/api/test").pipe(Effect.provide(mockLayer(200, { id: 7 }))),
    );
    expect(result).toEqual({ id: 7 });
  });

  test("apiGet is a function (real-fetch path covered by integration, not unit)", () => {
    expect(typeof apiGet).toBe("function");
  });
});
