import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";
import {
  backfillQuery,
  compactStore,
  fetchBackends,
  fetchHandoffs,
  fetchHealth,
  fetchProjects,
  fetchProjectScope,
  previewBackfill,
  runBackfill,
  scopeQuery,
  fetchOverview,
  fetchSettings,
  restart,
  saveSettings,
  settingsQuery,
  start,
  stop,
  testConnection,
} from "../api.js";
import { emptySettings } from "../settings.js";
import type { Settings } from "../types.js";

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

const reqOf = (ref: { req: HttpClientRequest.HttpClientRequest | null }) =>
  ref.req as unknown as HttpClientRequest.HttpClientRequest;

const SETTINGS: Settings = {
  ...emptySettings(),
  data_dir: "/srv/mem",
  port: 49374,
  enable_web: true,
  autostart_locked: true,
  backfill_max_sessions: 2000,
};

describe("agentmemory api", () => {
  test("fetchBackends lists the registered backends", async () => {
    const { ref, layer } = captureLayer({ backends: [{ id: "ai-memory", name: "ai-memory", has_data: true }] });
    const r = await Effect.runPromise(fetchBackends("/tools/agents").pipe(Effect.provide(layer)));
    expect(r.backends[0].id).toBe("ai-memory");
    expect(reqOf(ref).url).toContain("/tools/agents/agentmemory/backends");
  });

  // The panel is opened from a project menu with ?project=<wick id>. It must
  // ASK for the bucket rather than build a name from the id — the mapping has
  // one home, server-side (PLAN §22.2).
  test("fetchProjectScope asks the server which bucket a wick project uses", async () => {
    const { ref, layer } = captureLayer({
      project_id: "8c28230d",
      name: "Kasir",
      folder: "/srv/projects/8c28230d/files",
      workspace: "wick",
      project: "kasir-8c28230d",
      source: "marker",
    });
    const r = await Effect.runPromise(fetchProjectScope("/tools/agents", "8c28230d").pipe(Effect.provide(layer)));
    expect(r.workspace).toBe("wick");
    expect(r.project).toBe("kasir-8c28230d");
    expect(r.source).toBe("marker");
    expect(reqOf(ref).url).toContain("/tools/agents/agentmemory/project-scope?project=8c28230d");
    expect(reqOf(ref).method).toBe("GET");
  });

  test("fetchProjectScope escapes an id that would otherwise change the query", async () => {
    const { ref, layer } = captureLayer({});
    await Effect.runPromise(fetchProjectScope("/tools/agents", "a&b=c").pipe(Effect.provide(layer)));
    expect(reqOf(ref).url).toContain("project=a%26b%3Dc");
  });

  test("fetchOverview GETs the per-backend status endpoint", async () => {
    const { ref, layer } = captureLayer({ backend: { id: "ai-memory" }, daemon: { state: "running" } });
    await Effect.runPromise(fetchOverview("/tools/agents", "ai-memory").pipe(Effect.provide(layer)));
    expect(reqOf(ref).url).toContain("/tools/agents/agentmemory/ai-memory/status");
    expect(reqOf(ref).method).toBe("GET");
  });

  test("fetchOverview parses the daemon + store blocks", async () => {
    const ov = await Effect.runPromise(
      fetchOverview("/tools/agents", "ai-memory").pipe(
        Effect.provide(
          mockLayer(200, {
            backend: { id: "ai-memory", name: "ai-memory", has_data: true },
            daemon: { state: "running", running: true, version: "2.4.0", bound_port: 49374 },
            store: { counts: { pages_latest: 4, pages_all: 6, sessions: 3, observations: 40 } },
          }),
        ),
      ),
    );
    expect(ov.daemon.state).toBe("running");
    expect(ov.store?.counts.observations).toBe(40);
  });

  test("an unreadable store still answers 200, carrying the named reason", async () => {
    const ov = await Effect.runPromise(
      fetchOverview("/tools/agents", "ai-memory").pipe(
        Effect.provide(
          mockLayer(200, {
            backend: { id: "ai-memory" },
            daemon: { state: "running" },
            store_error: "web API disabled",
            store_reason: "web_disabled",
          }),
        ),
      ),
    );
    expect(ov.store).toBeUndefined();
    expect(ov.store_reason).toBe("web_disabled");
  });

  test("start/stop/restart POST their per-backend endpoints", async () => {
    for (const [fn, path] of [
      [start, "start"],
      [stop, "stop"],
      [restart, "restart"],
    ] as const) {
      const { ref, layer } = captureLayer({ state: "running" });
      await Effect.runPromise(fn("/tools/agents", "ai-memory").pipe(Effect.provide(layer)));
      expect(reqOf(ref).method).toBe("POST");
      expect(reqOf(ref).url).toContain(`/agentmemory/ai-memory/${path}`);
    }
  });

  test("testConnection POSTs and returns the probe result", async () => {
    const r = await Effect.runPromise(
      testConnection("/tools/agents", "ai-memory").pipe(
        Effect.provide(mockLayer(200, { ok: true, base_url: "http://127.0.0.1:49374", health_path: "/healthz", version: "2.4.0" })),
      ),
    );
    expect(r.ok).toBe(true);
    expect(r.version).toBe("2.4.0");
  });

  test("fetchSettings GETs settings with its autostart lock", async () => {
    const { ref, layer } = captureLayer({ settings: SETTINGS, autostart_lock: { locked: true, reason: "claude uses it" } });
    const r = await Effect.runPromise(fetchSettings("/tools/agents", "ai-memory").pipe(Effect.provide(layer)));
    expect(reqOf(ref).url).toContain("/agentmemory/ai-memory/settings");
    expect(r.autostart_lock.locked).toBe(true);
  });

  test("saveSettings posts every field in the query string", async () => {
    const { ref, layer } = captureLayer({ settings: SETTINGS, autostart_lock: { locked: false } });
    await Effect.runPromise(saveSettings("/tools/agents", "ai-memory", SETTINGS).pipe(Effect.provide(layer)));
    const url = reqOf(ref).url;
    expect(reqOf(ref).method).toBe("POST");
    expect(url).toContain("data_dir=%2Fsrv%2Fmem");
    expect(url).toContain("port=49374");
    expect(url).toContain("enable_web=true");
    expect(url).toContain("backfill_max_sessions=2000");
  });

  test("settingsQuery sends false explicitly so a switched-off flag can be saved", () => {
    const q = settingsQuery({ ...SETTINGS, enable_web: false, autostart: false, data_dir: "" });
    expect(q).toContain("enable_web=false");
    expect(q).toContain("autostart=false");
    expect(q).toContain("data_dir=");
  });

  test("settingsQuery never posts the derived autostart lock back", () => {
    expect(settingsQuery(SETTINGS)).not.toContain("autostart_locked");
  });

  test("fails with APIError on non-2xx", async () => {
    const err = await Effect.runPromise(
      fetchOverview("/tools/agents", "ai-memory").pipe(Effect.flip, Effect.provide(mockLayer(403, { error: "forbidden" }))),
    );
    expect(err).toBeInstanceOf(APIError);
    expect(err.status).toBe(403);
  });
});

// ── slice 4B endpoints ───────────────────────────────────────────────

describe("panel data endpoints", () => {
  test("fetchProjects hits the projects route", async () => {
    const { ref, layer } = captureLayer({ projects: [] });
    await Effect.runPromise(fetchProjects("/tools/agents", "ai-memory").pipe(Effect.provide(layer)));
    expect(reqOf(ref).url).toContain("/agentmemory/ai-memory/projects");
  });

  // A blank workspace/project means "store-wide" on the Go side, so an empty
  // scope must send no keys at all — a stray `project=` would silently widen
  // a read that was meant to be about one project.
  test("scopeQuery omits empty keys rather than sending them blank", () => {
    expect(scopeQuery(undefined)).toBe("");
    expect(scopeQuery({})).toBe("");
    expect(scopeQuery({ workspace: "", project: "" })).toBe("");
    expect(scopeQuery({ workspace: "default", project: "files" })).toBe("?workspace=default&project=files");
  });

  test("fetchHealth and fetchHandoffs carry the scope", async () => {
    const h = captureLayer({ doctor: {}, contamination: { sessions_misbucketed: 0 } });
    await Effect.runPromise(
      fetchHealth("/tools/agents", "ai-memory", { workspace: "default", project: "files" }).pipe(Effect.provide(h.layer)),
    );
    expect(reqOf(h.ref).url).toContain("/health?workspace=default&project=files");

    const k = captureLayer({ handoffs: [] });
    await Effect.runPromise(
      fetchHandoffs("/tools/agents", "ai-memory", { project: "files" }).pipe(Effect.provide(k.layer)),
    );
    expect(reqOf(k.ref).url).toContain("/handoffs?project=files");
  });
});

describe("backfill", () => {
  test("backfillQuery only sends the dangerous flags when they are set", () => {
    expect(backfillQuery({ project: "files" })).toBe("?project=files");
    const q = backfillQuery({ project: "files", force: true, confirm: true, session: "s1", max_sessions: 2000 });
    expect(q).toContain("force=true");
    expect(q).toContain("confirm=true");
    expect(q).toContain("session=s1");
    expect(q).toContain("max_sessions=2000");
  });

  // A preview that needed the flag which multiplies stored observations in
  // order to be honest would be no preview at all (PLAN §11.1).
  test("previewBackfill strips force and confirm even when handed them", async () => {
    const { ref, layer } = captureLayer({ report: { dry_run: true } });
    await Effect.runPromise(
      previewBackfill("/tools/agents", "ai-memory", { project: "files", force: true, confirm: true }).pipe(
        Effect.provide(layer),
      ),
    );
    const url = reqOf(ref).url;
    expect(url).toContain("/backfill/preview");
    expect(url).not.toContain("force=true");
    expect(url).not.toContain("confirm=true");
  });

  test("runBackfill passes what it was given, unchanged", async () => {
    const { ref, layer } = captureLayer({ report: { dry_run: false } });
    await Effect.runPromise(
      runBackfill("/tools/agents", "ai-memory", { project: "files" }).pipe(Effect.provide(layer)),
    );
    expect(reqOf(ref).url).toContain("/backfill/run?project=files");
    expect(reqOf(ref).method).toBe("POST");
  });
});

test("compactStore always carries the confirmation the server demands", async () => {
  const { ref, layer } = captureLayer({ report: { output: "Compacted: 1.0 MiB → 1020.0 KiB (24.0 KiB reclaimed)." } });
  await Effect.runPromise(compactStore("/tools/agents", "ai-memory").pipe(Effect.provide(layer)));
  expect(reqOf(ref).url).toContain("/compact?confirm=true");
  expect(reqOf(ref).method).toBe("POST");
});
