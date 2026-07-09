import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";
import {
  listSchedules,
  createSchedule,
  cancelSchedule,
  pauseSchedule,
  resumeSchedule,
  rescheduleSchedule,
} from "../schedules.js";
import type { Schedule } from "../../types/agents.js";

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

const SCHED: Schedule = {
  id: "sm_1",
  session_id: "sess-1",
  created_by: "ai",
  kind: "once",
  run_at: "2026-07-09T12:40:00Z",
  status: "pending",
  message: "check in",
  run_count: 0,
};

describe("listSchedules", () => {
  test("unwraps the schedules array", async () => {
    const out = await Effect.runPromise(
      listSchedules("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { schedules: [SCHED] })),
      ),
    );
    expect(out).toHaveLength(1);
    expect(out[0].id).toBe("sm_1");
  });

  test("missing array → empty", async () => {
    const out = await Effect.runPromise(
      listSchedules("/tools/agents", "sess-1").pipe(Effect.provide(mockLayer(200, {}))),
    );
    expect(out).toEqual([]);
  });
});

describe("createSchedule", () => {
  test("returns the created schedule on 2xx (one-shot)", async () => {
    const out = await Effect.runPromise(
      createSchedule("/tools/agents", "sess-1", { message: "hi", runAt: "+2h" }).pipe(
        Effect.provide(mockLayer(200, SCHED)),
      ),
    );
    expect(out.id).toBe("sm_1");
  });

  test("recurring create (every)", async () => {
    const out = await Effect.runPromise(
      createSchedule("/tools/agents", "sess-1", { message: "poll", every: "5m", maxRuns: 10 }).pipe(
        Effect.provide(mockLayer(200, { ...SCHED, kind: "recurring", interval_ms: 300000 })),
      ),
    );
    expect(out.kind).toBe("recurring");
  });

  test("surfaces APIError on 4xx", async () => {
    const err = await Effect.runPromise(
      createSchedule("/tools/agents", "sess-1", { message: "hi", runAt: "bad" }).pipe(
        Effect.flip,
        Effect.provide(mockLayer(400, { error: "run_at is in the past" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
  });
});

describe("lifecycle actions", () => {
  test("cancel resolves on 2xx", async () => {
    const out = await Effect.runPromise(
      cancelSchedule("/tools/agents", "sess-1", "sm_1").pipe(
        Effect.provide(mockLayer(200, { id: "sm_1", status: "cancelled" })),
      ),
    );
    expect(out.status).toBe("cancelled");
  });

  test("pause returns the row", async () => {
    const out = await Effect.runPromise(
      pauseSchedule("/tools/agents", "sess-1", "sm_1").pipe(
        Effect.provide(mockLayer(200, { ...SCHED, kind: "recurring", paused: true })),
      ),
    );
    expect(out.paused).toBe(true);
  });

  test("resume returns the row", async () => {
    const out = await Effect.runPromise(
      resumeSchedule("/tools/agents", "sess-1", "sm_1").pipe(
        Effect.provide(mockLayer(200, { ...SCHED, kind: "recurring", paused: false })),
      ),
    );
    expect(out.paused).toBe(false);
  });

  test("reschedule sends edit + returns row", async () => {
    const out = await Effect.runPromise(
      rescheduleSchedule("/tools/agents", "sess-1", "sm_1", { every: "10m" }).pipe(
        Effect.provide(mockLayer(200, { ...SCHED, kind: "recurring", interval_ms: 600000 })),
      ),
    );
    expect(out.interval_ms).toBe(600000);
  });
});
