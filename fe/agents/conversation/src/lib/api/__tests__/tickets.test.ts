import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import {
  addNote,
  attachSession,
  createTicket,
  deleteNote,
  detachSession,
  getProjectTickets,
  getTicket,
  getTicketFilter,
  listNotes,
  saveTicketFilter,
  updateNote,
  updateTicket,
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

// Capture method + URL so the scope/verb contract can be asserted.
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

const BASE = "/tools/agents";

describe("tickets api", () => {
  test("getProjectTickets fills defaults for a sparse payload", async () => {
    const out = await Effect.runPromise(
      getProjectTickets(BASE, "p1").pipe(
        Effect.provide(mockLayer(200, { config: { enabled: true } })),
      ),
    );
    expect(out.tickets).toEqual([]);
    expect(out.statuses).toEqual(["open", "in_progress", "waiting", "done"]);
    expect(out.users).toEqual({});
  });

  test("createTicket POSTs under the project", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      createTicket(BASE, "p1", { title: "Webhook 401" }).pipe(
        Effect.provide(captureLayer(captured, { id: "T-4F2A" })),
      ),
    );
    expect(captured.method).toBe("POST");
    expect(captured.url).toContain("/api/projects/p1/tickets");
  });

  test("getTicket fills sessions and notes defaults", async () => {
    const out = await Effect.runPromise(
      getTicket(BASE, "T-4F2A").pipe(
        Effect.provide(mockLayer(200, { ticket: { id: "T-4F2A" }, config: {} })),
      ),
    );
    expect(out.sessions).toEqual([]);
    expect(out.notes).toEqual([]);
  });

  test("updateTicket PATCHes the ticket by its own id", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      updateTicket(BASE, "T-4F2A", { status: "done" }).pipe(
        Effect.provide(captureLayer(captured, { id: "T-4F2A", status: "done" })),
      ),
    );
    expect(captured.method).toBe("PATCH");
    expect(captured.url).toContain("/api/tickets/T-4F2A");
  });

  test("attach and detach address the session under the ticket", async () => {
    const a: { method?: string; url?: string } = {};
    await Effect.runPromise(
      attachSession(BASE, "T-4F2A", "sess-1").pipe(
        Effect.provide(captureLayer(a, { status: "ok" })),
      ),
    );
    expect(a.method).toBe("PUT");
    expect(a.url).toContain("/api/tickets/T-4F2A/sessions/sess-1");

    const d: { method?: string; url?: string } = {};
    await Effect.runPromise(
      detachSession(BASE, "T-4F2A", "sess-1").pipe(
        Effect.provide(captureLayer(d, { status: "ok" })),
      ),
    );
    expect(d.method).toBe("DELETE");
    expect(d.url).toContain("/api/tickets/T-4F2A/sessions/sess-1");
  });

  test("saveTicketFilter surfaces APIError on 401", async () => {
    const err = await Effect.runPromise(
      saveTicketFilter(BASE, "p1", { assignee: "me" }).pipe(
        Effect.flip,
        Effect.provide(mockLayer(401, { error: "login required" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(401);
  });

  test("getTicketFilter returns an empty filter for a null body", async () => {
    const out = await Effect.runPromise(
      getTicketFilter(BASE, "p1").pipe(Effect.provide(mockLayer(200, null))),
    );
    expect(out).toEqual({});
  });
});

describe("notes api", () => {
  test("a session scope becomes ?session_id — the server resolves it to the ticket", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      listNotes(BASE, { sessionId: "sess-1" }).pipe(
        Effect.provide(captureLayer(captured, { notes: [] })),
      ),
    );
    expect(captured.method).toBe("GET");
    expect(captured.url).toContain("/api/notes?session_id=sess-1");
  });

  test("a ticket scope becomes ?ticket_id", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      listNotes(BASE, { ticketId: "T-4F2A" }).pipe(
        Effect.provide(captureLayer(captured, { notes: [] })),
      ),
    );
    expect(captured.url).toContain("/api/notes?ticket_id=T-4F2A");
  });

  test("listNotes fills defaults for a sparse payload", async () => {
    const out = await Effect.runPromise(
      listNotes(BASE, { sessionId: "s1" }).pipe(Effect.provide(mockLayer(200, {}))),
    );
    expect(out.notes).toEqual([]);
    expect(out.users).toEqual({});
  });

  test("addNote POSTs with the scope on the query string", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      addNote(BASE, { ticketId: "T-4F2A" }, { body: "found it", checkable: true }).pipe(
        Effect.provide(captureLayer(captured, { id: "n1", body: "found it" })),
      ),
    );
    expect(captured.method).toBe("POST");
    expect(captured.url).toContain("/api/notes?ticket_id=T-4F2A");
  });

  test("updateNote carries the note id in the path and the scope in the query", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      updateNote(BASE, { sessionId: "s1" }, "n1", { hidden: true }).pipe(
        Effect.provide(captureLayer(captured, { id: "n1", hidden: true })),
      ),
    );
    expect(captured.method).toBe("PATCH");
    expect(captured.url).toContain("/api/notes/n1?session_id=s1");
  });

  test("deleteNote DELETEs the note within its scope", async () => {
    const captured: { method?: string; url?: string } = {};
    await Effect.runPromise(
      deleteNote(BASE, { ticketId: "T-4F2A" }, "n1").pipe(
        Effect.provide(captureLayer(captured, { status: "ok" })),
      ),
    );
    expect(captured.method).toBe("DELETE");
    expect(captured.url).toContain("/api/notes/n1?ticket_id=T-4F2A");
  });
});
