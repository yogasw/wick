import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { listSessions, getConversation, getSessionMeta } from "../sessions.js";
import type { SessionListItem, ConversationTurn, SessionMeta } from "../../types/agents.js";

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

const SESSION: SessionListItem = {
  id: "sess-1",
  label: "Test session",
  status: "idle",
  project_id: "proj-1",
  active_agent: "claude",
  created_at: "2026-01-01T00:00:00Z",
  last_active: "2026-01-02T00:00:00Z",
  lifecycle: "persistent",
  pid: 1234,
};

const TURN: ConversationTurn = {
  turn_id: "turn-1",
  role: "assistant",
  agent: "claude",
  provider: "anthropic",
  text: "Hello",
  timestamp: 1700000000,
  truncated: false,
  interrupted: false,
  has_trace: false,
  events: [],
  attachments: [],
};

const META: SessionMeta = {
  id: "sess-1",
  label: "Test session",
  status: "idle",
  project_id: "proj-1",
  active_agent: "claude",
  title_custom: false,
  created_at: "2026-01-01T00:00:00Z",
  last_active: "2026-01-02T00:00:00Z",
};

describe("listSessions", () => {
  test("parses sessions array from response", async () => {
    const result = await Effect.runPromise(
      listSessions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, { sessions: [SESSION] })),
      ),
    );
    expect(result.sessions).toHaveLength(1);
    expect(result.sessions[0].id).toBe("sess-1");
    expect(result.sessions[0].label).toBe("Test session");
    expect(result.sessions[0].lifecycle).toBe("persistent");
  });

  test("returns empty sessions array", async () => {
    const result = await Effect.runPromise(
      listSessions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, { sessions: [] })),
      ),
    );
    expect(result.sessions).toHaveLength(0);
  });
});

describe("getConversation", () => {
  test("parses turns array from response", async () => {
    const result = await Effect.runPromise(
      getConversation("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { turns: [TURN] })),
      ),
    );
    expect(result.turns).toHaveLength(1);
    expect(result.turns[0].turn_id).toBe("turn-1");
    expect(result.turns[0].role).toBe("assistant");
    expect(result.turns[0].events).toEqual([]);
    expect(result.turns[0].attachments).toEqual([]);
  });

  test("returns empty turns array", async () => {
    const result = await Effect.runPromise(
      getConversation("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, { turns: [] })),
      ),
    );
    expect(result.turns).toHaveLength(0);
  });
});

describe("getConversation - null array normalization (Go nil → JSON null)", () => {
  test("normalizes null events and attachments to empty arrays", async () => {
    const result = await Effect.runPromise(
      getConversation("/tools/agents", "sess-1").pipe(
        Effect.provide(
          mockLayer(200, {
            turns: [
              {
                turn_id: "1",
                role: "assistant",
                agent: "claude",
                provider: "anthropic",
                text: "hi",
                timestamp: 0,
                truncated: false,
                interrupted: false,
                has_trace: false,
                events: null,
                attachments: null,
              },
            ],
          }),
        ),
      ),
    );
    expect(result.turns[0].events).toEqual([]);
    expect(result.turns[0].attachments).toEqual([]);
  });
});

describe("listSessions - null array normalization (Go nil → JSON null)", () => {
  test("normalizes null sessions array to empty array", async () => {
    const result = await Effect.runPromise(
      listSessions("/tools/agents").pipe(
        Effect.provide(mockLayer(200, { sessions: null })),
      ),
    );
    expect(result.sessions).toEqual([]);
  });
});

describe("getSessionMeta", () => {
  test("parses session meta from response", async () => {
    const result = await Effect.runPromise(
      getSessionMeta("/tools/agents", "sess-1").pipe(
        Effect.provide(mockLayer(200, META)),
      ),
    );
    expect(result.id).toBe("sess-1");
    expect(result.title_custom).toBe(false);
    expect(result.active_agent).toBe("claude");
  });
});

describe("listSessions — project scoping", () => {
  test("requests /api/sessions?project=proj1 when projectId is provided", async () => {
    let capturedReq: HttpClientRequest.HttpClientRequest | null = null;
    const captureLayer = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        capturedReq = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify({ sessions: [] }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        );
      }),
    );

    await Effect.runPromise(
      listSessions("/tools/agents", "proj1").pipe(Effect.provide(captureLayer)),
    );

    const r = capturedReq as unknown as HttpClientRequest.HttpClientRequest;
    expect(r.url).toContain("project=proj1");
  });

  test("requests /api/sessions without project param when projectId is omitted", async () => {
    let capturedReq: HttpClientRequest.HttpClientRequest | null = null;
    const captureLayer = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        capturedReq = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify({ sessions: [] }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        );
      }),
    );

    await Effect.runPromise(
      listSessions("/tools/agents").pipe(Effect.provide(captureLayer)),
    );

    const r = capturedReq as unknown as HttpClientRequest.HttpClientRequest;
    expect(r.url).not.toContain("project=");
  });
});
