import { describe, it, expect } from "vitest";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { Effect, Layer } from "effect";
import { APIError } from "@wick-fe/common-api";
import { fetchReportE, fetchSeriesE } from "../api.js";

// A mock HttpClient layer, so these run with no network and no server.
const mockLayer = (status: number, body: unknown) =>
  Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) =>
      Effect.succeed(
        HttpClientResponse.fromWeb(req, new Response(JSON.stringify(body), { status })),
      ),
    ),
  );

describe("fetchReportE", () => {
  // Go omits empty slices from JSON, so `agents` arrives as null on an
  // idle machine. Without normalising, every `.length` in the page throws.
  it("normalises a null agents list to an empty array", async () => {
    const out = await Effect.runPromise(
      fetchReportE("/tools/agents").pipe(
        Effect.provide(mockLayer(200, { agents: null, mode: "off", machine_known: true })),
      ),
    );
    expect(out.agents).toEqual([]);
  });

  it("passes a populated list through untouched", async () => {
    const agents = [{ name: "claude", pid: 1, tree_bytes: 100, procs: 2, cpu_pct: 5, io_read_bps: 0, io_write_bps: 0 }];
    const out = await Effect.runPromise(
      fetchReportE("/tools/agents").pipe(Effect.provide(mockLayer(200, { agents, mode: "measure" }))),
    );
    expect(out.agents).toHaveLength(1);
    expect(out.mode).toBe("measure");
  });

  it("surfaces a non-2xx as a typed APIError", async () => {
    const err = await Effect.runPromise(
      fetchReportE("/tools/agents").pipe(
        Effect.flip,
        Effect.provide(mockLayer(403, { error: "admin only" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(403);
  });
});

describe("fetchSeriesE", () => {
  // Same null-slice problem, and the charts map over both arrays.
  it("normalises null machine and agents series", async () => {
    const out = await Effect.runPromise(
      fetchSeriesE("/tools/agents", 30).pipe(
        Effect.provide(mockLayer(200, { enabled: true, machine: null, agents: null })),
      ),
    );
    expect(out.machine).toEqual([]);
    expect(out.agents).toEqual([]);
  });

  // History switched off is a supported configuration, not an error: the
  // page falls back to the live snapshot and says so.
  it("reports disabled history without failing", async () => {
    const out = await Effect.runPromise(
      fetchSeriesE("/tools/agents", 30).pipe(Effect.provide(mockLayer(200, { enabled: false }))),
    );
    expect(out.enabled).toBe(false);
    expect(out.machine).toEqual([]);
  });

  it("puts the requested window in the query string", async () => {
    let seen = "";
    const capture = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        seen = req.url;
        return Effect.succeed(
          HttpClientResponse.fromWeb(req, new Response(JSON.stringify({ enabled: true }), { status: 200 })),
        );
      }),
    );
    await Effect.runPromise(fetchSeriesE("/tools/agents", 120).pipe(Effect.provide(capture)));
    expect(seen).toContain("minutes=120");
  });
});
