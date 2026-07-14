import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { listWorkspace, addWorkspace } from "../workspace.js";
import type { WsInstance, WsBase } from "../../types/agents.js";

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

const INSTANCE: WsInstance = {
  id: "cid-1",
  label: "Test Conn",
  status: "ready",
  fields: [
    { key: "url", label: "URL", type: "text", value: "https://example.com" },
    { key: "secret", label: "Secret", type: "text", secret: true, set: true },
  ],
};

const BASE: WsBase = { base_key: "slack", label: "Slack" };

describe("listWorkspace", () => {
  test("parses instances and bases from response", async () => {
    const result = await Effect.runPromise(
      listWorkspace("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { instances: [INSTANCE], bases: [BASE] })),
      ),
    );
    expect(result.instances).toHaveLength(1);
    expect(result.instances[0].id).toBe("cid-1");
    expect(result.instances[0].status).toBe("ready");
    expect(result.bases).toHaveLength(1);
    expect(result.bases[0].base_key).toBe("slack");
  });

  test("returns empty arrays when workspace is empty", async () => {
    const result = await Effect.runPromise(
      listWorkspace("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { instances: [], bases: [] })),
      ),
    );
    expect(result.instances).toHaveLength(0);
    expect(result.bases).toHaveLength(0);
  });

  test("WsInstance fields include secret flag", async () => {
    const result = await Effect.runPromise(
      listWorkspace("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { instances: [INSTANCE], bases: [] })),
      ),
    );
    const secretField = result.instances[0].fields?.find((f) => f.key === "secret");
    expect(secretField?.secret).toBe(true);
  });
});

describe("addWorkspace", () => {
  test("returns the new WsInstance", async () => {
    const result = await Effect.runPromise(
      addWorkspace("/tools/agents", "sess-1", "slack").pipe(
        Effect.provide(mockLayer(200, INSTANCE)),
      ),
    );
    expect(result.id).toBe("cid-1");
    expect(result.label).toBe("Test Conn");
  });
});

describe("listWorkspace tombstones", () => {
  test("parses deleted tombstones from response", async () => {
    const result = await Effect.runPromise(
      listWorkspace("/tools/agents", "sess-1").pipe(
        Effect.provide(
          mockLayer(200, {
            instances: [],
            bases: [],
            deleted: [{ label: "Staging", base_key: "httprest", deleted_at: "2026-07-13T12:00:00Z", reason: "session idle" }],
          }),
        ),
      ),
    );
    expect(result.deleted).toHaveLength(1);
    expect(result.deleted[0].label).toBe("Staging");
    expect(result.deleted[0].reason).toBe("session idle");
  });

  test("defaults deleted to empty array when absent", async () => {
    const result = await Effect.runPromise(
      listWorkspace("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { instances: [], bases: [] })),
      ),
    );
    expect(result.deleted).toEqual([]);
  });
});
