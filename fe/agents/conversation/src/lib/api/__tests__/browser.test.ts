import { describe, test, expect, beforeEach } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { listInstances, listSessions, wsURL } from "../browser.js";

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

describe("listInstances", () => {
  test("maps rows to instances, falling back label→id", async () => {
    const out = await Effect.runPromise(
      listInstances().pipe(
        Effect.provide(
          mockLayer(200, {
            rows: [
              { id: "c1", label: "Prod browser", disabled: false },
              { id: "c2", label: "", disabled: true },
              { id: "c3" },
            ],
          }),
        ),
      ),
    );
    expect(out).toEqual([
      { id: "c1", label: "Prod browser", disabled: false },
      { id: "c2", label: "c2", disabled: true },
      { id: "c3", label: "c3", disabled: false },
    ]);
  });

  test("empty rows → empty list", async () => {
    const out = await Effect.runPromise(
      listInstances().pipe(Effect.provide(mockLayer(200, {}))),
    );
    expect(out).toEqual([]);
  });
});

describe("listSessions", () => {
  test("unwraps the sessions array", async () => {
    const out = await Effect.runPromise(
      listSessions("c1").pipe(
        Effect.provide(
          mockLayer(200, {
            sessions: [
              { session_id: "s1", pid: 10, browser: "chromium", created: "", tabs: [] },
            ],
            count: 1,
          }),
        ),
      ),
    );
    expect(out).toHaveLength(1);
    expect(out[0].session_id).toBe("s1");
  });

  test("missing sessions → empty list", async () => {
    const out = await Effect.runPromise(
      listSessions("c1").pipe(Effect.provide(mockLayer(200, {}))),
    );
    expect(out).toEqual([]);
  });
});

describe("wsURL", () => {
  beforeEach(() => {
    // jsdom default location is http://localhost:3000 (or similar); pin it.
    Object.defineProperty(window, "location", {
      value: new URL("https://wick.example.com/tools/agents/x"),
      writable: true,
    });
  });

  test("builds a wss URL under https with encoded params", () => {
    const u = wsURL("c 1", "s/1", 2);
    expect(u).toBe(
      "wss://wick.example.com/manager/api/connectors/playwright_browser/c%201/browser/ws?session=s%2F1&tab=2",
    );
  });

  test("uses ws:// under http", () => {
    Object.defineProperty(window, "location", {
      value: new URL("http://localhost:8080/x"),
      writable: true,
    });
    const u = wsURL("c1", "s1", 0);
    expect(u.startsWith("ws://localhost:8080/")).toBe(true);
  });
});
