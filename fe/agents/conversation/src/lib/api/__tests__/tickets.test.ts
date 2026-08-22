import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import {
  getProjectTickets,
  updateSessionTicket,
  getTicketFilter,
  saveTicketFilter,
} from "../tickets.js";
import { APIError } from "@wick-fe/common-api";

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

// Capture the outgoing request so method/URL can be asserted.
const captureLayer = (captured: { method?: string; url?: string }, body: unknown) =>
  Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) => {
      captured.method = req.method;
      captured.url = req.url;
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

describe("tickets api", () => {
  test("getProjectTickets fills defaults for empty payload", async () => {
    const out = await Effect.runPromise(
      getProjectTickets("/tools/agents", "p1").pipe(
        Effect.provide(mockLayer(200, { config: { enabled: true } })),
      ),
    );
    expect(out.tickets).toEqual([]);
    expect(out.statuses).toEqual(["open", "in_progress", "waiting", "done"]);
    expect(out.users).toEqual({});
  });

  test("updateSessionTicket PUTs to the session ticket endpoint", async () => {
    const captured: { method?: string; url?: string } = {};
    const out = await Effect.runPromise(
      updateSessionTicket("/tools/agents", "s1", { status: "done" }).pipe(
        Effect.provide(captureLayer(captured, { status: "ok", ticket: { status: "done" } })),
      ),
    );
    expect(captured.method).toBe("PUT");
    expect(captured.url).toContain("/api/sessions/s1/ticket");
    expect(out.status).toBe("ok");
  });

  test("getTicketFilter returns empty object for null body", async () => {
    const out = await Effect.runPromise(
      getTicketFilter("/tools/agents", "p1").pipe(Effect.provide(mockLayer(200, null))),
    );
    expect(out).toEqual({});
  });

  test("saveTicketFilter surfaces APIError on 401", async () => {
    const err = await Effect.runPromise(
      saveTicketFilter("/tools/agents", "p1", { assignee: "me" }).pipe(
        Effect.flip,
        Effect.provide(mockLayer(401, { error: "login required" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(401);
  });
});
