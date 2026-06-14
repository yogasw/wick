import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
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
