import { describe, it, expect } from "vitest";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { Effect, Layer } from "effect";
import { APIError } from "@wick-fe/common-api";
import {
  fetchReportE,
  fetchSeriesE,
  fetchWrapperStatusE,
  uninstallWrapperE,
} from "../api.js";

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

describe("fetchWrapperStatusE", () => {
  // Go omits empty slices, so a machine with nothing running sends null
  // for both lists. Without normalising, the panel's .filter and .some
  // calls throw on the exact machine that is least interesting.
  it("normalises null lists so an idle machine renders", async () => {
    const out = await Effect.runPromise(
      fetchWrapperStatusE("/tools/agents").pipe(
        Effect.provide(
          mockLayer(200, {
            supported: true,
            providers: null,
            processes: null,
            isolated: 0,
            unisolated: 0,
          }),
        ),
      ),
    );

    expect(out.providers).toEqual([]);
    expect(out.processes).toEqual([]);
  });

  // An unsupported platform is a supported configuration, not an error:
  // the panel renders the reason instead of controls that cannot work.
  it("keeps the notice when the platform has no cgroups", async () => {
    const out = await Effect.runPromise(
      fetchWrapperStatusE("/tools/agents").pipe(
        Effect.provide(
          mockLayer(200, {
            supported: false,
            notice: "agent isolation status requires Linux",
            isolated: 0,
            unisolated: 0,
          }),
        ),
      ),
    );

    expect(out.supported).toBe(false);
    expect(out.notice).toContain("Linux");
  });
});

describe("uninstallWrapperE", () => {
  // Two calls by design: the first returns the command that restores the
  // system path, the second removes the shim. Reversed, there is a window
  // where the path points at a file that no longer exists and every spawn
  // fails — so the unconfirmed call must NOT remove anything.
  it("asks for the restore command before removing anything", async () => {
    const out = await Effect.runPromise(
      uninstallWrapperE("/tools/agents", {}).pipe(
        Effect.provide(
          mockLayer(200, {
            commands: ["sudo ln -sfn /opt/x/claude /usr/local/bin/claude"],
            message: "Run this first",
          }),
        ),
      ),
    );

    expect(out.commands?.[0]).toContain("ln -sfn");
    expect(out.written).toBeUndefined();
  });
});
