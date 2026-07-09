import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";
import { listAll, cancelById, pauseById, resumeById, type Schedule } from "../api.js";

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

const S: Schedule = {
  id: "sm_1",
  session_id: "sess-1",
  session_label: "Ops review",
  created_by: "ai",
  kind: "recurring",
  run_at: "2026-07-09T12:40:00Z",
  status: "active",
  message: "cek loki",
  run_count: 2,
  interval_ms: 300000,
};

describe("listAll", () => {
  test("unwraps schedules array", async () => {
    const out = await Effect.runPromise(
      listAll("/tools/agents").pipe(Effect.provide(mockLayer(200, { schedules: [S] }))),
    );
    expect(out).toHaveLength(1);
    expect(out[0].session_label).toBe("Ops review");
  });

  test("missing array → empty", async () => {
    const out = await Effect.runPromise(
      listAll("/tools/agents").pipe(Effect.provide(mockLayer(200, {}))),
    );
    expect(out).toEqual([]);
  });
});

describe("by-id actions", () => {
  test("cancel returns row", async () => {
    const out = await Effect.runPromise(
      cancelById("/tools/agents", "sm_1").pipe(
        Effect.provide(mockLayer(200, { ...S, status: "cancelled" })),
      ),
    );
    expect(out.status).toBe("cancelled");
  });

  test("pause returns row", async () => {
    const out = await Effect.runPromise(
      pauseById("/tools/agents", "sm_1").pipe(Effect.provide(mockLayer(200, { ...S, paused: true }))),
    );
    expect(out.paused).toBe(true);
  });

  test("resume surfaces APIError on 4xx", async () => {
    const err = await Effect.runPromise(
      resumeById("/tools/agents", "sm_1").pipe(
        Effect.flip,
        Effect.provide(mockLayer(404, { error: "schedule not found" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
  });
});
