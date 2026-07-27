import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientResponse } from "@effect/platform";
import { getSessionOverrides, setSessionOverride } from "../overrides.js";

// A mock layer that records the request it saw, so we can assert URL + method +
// body (proving the popover SAVES via this endpoint and never sends a message).
function captureLayer(status: number, body: unknown) {
  const seen: { url?: string; method?: string } = {};
  const layer = Layer.succeed(
    HttpClient.HttpClient,
    HttpClient.make((req) => {
      seen.url = req.url;
      seen.method = req.method;
      return Effect.succeed(
        HttpClientResponse.fromWeb(
          req,
          new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
        ),
      );
    }),
  );
  return { layer, seen };
}

describe("getSessionOverrides", () => {
  test("GETs the session overrides endpoint with provider query", async () => {
    const { layer, seen } = captureLayer(200, {
      schema: [{ Key: "thinking_on", Type: "checkbox" }],
      values: { thinking_on: "true" },
    });
    const res = await Effect.runPromise(getSessionOverrides("/tools/agents", "sess-1", "wick").pipe(Effect.provide(layer)));
    expect(res.schema[0].Key).toBe("thinking_on");
    expect(res.values.thinking_on).toBe("true");
    expect(seen.method).toBe("GET");
    expect(seen.url).toContain("/providers/wick/sessions/sess-1/overrides");
    expect(seen.url).toContain("provider=wick");
  });
});

describe("setSessionOverride", () => {
  test("POSTs a single {key,value} — no chat message", async () => {
    const { layer, seen } = captureLayer(200, { values: { reasoning_effort: "high" } });
    const res = await Effect.runPromise(
      setSessionOverride("/tools/agents", "sess-1", "reasoning_effort", "high", "wick").pipe(Effect.provide(layer)),
    );
    expect(res.values.reasoning_effort).toBe("high");
    expect(seen.method).toBe("POST");
    // Saves through the overrides endpoint — NOT the /send message path.
    expect(seen.url).toContain("/providers/wick/sessions/sess-1/overrides");
    expect(seen.url).not.toContain("/send");
  });
});
