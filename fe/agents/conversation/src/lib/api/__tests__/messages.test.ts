import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { sendMessage } from "../messages.js";

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

describe("sendMessage", () => {
  test("posts JSON with text field when no files provided", async () => {
    let capturedReq: HttpClientRequest.HttpClientRequest | null = null;

    const capturingLayer = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        capturedReq = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify({ status: "queued" }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        );
      }),
    );

    await Effect.runPromise(
      sendMessage("/tools/agents", "sess-1", { text: "hello" }).pipe(
        Effect.provide(capturingLayer),
      ),
    );

    expect(capturedReq).not.toBeNull();
    const req = capturedReq as unknown as HttpClientRequest.HttpClientRequest;
    expect(req.url).toContain("/sessions/sess-1/send");
    expect(req.method).toBe("POST");
  });

  test("returns queued status on success", async () => {
    const result = await Effect.runPromise(
      sendMessage("/tools/agents", "sess-1", { text: "hi" }).pipe(
        Effect.provide(mockLayer(200, { status: "queued" })),
      ),
    );
    expect(result.status).toBe("queued");
  });

  test("fails with APIError on non-2xx response", async () => {
    const exit = await Effect.runPromiseExit(
      sendMessage("/tools/agents", "sess-1", { text: "fail" }).pipe(
        Effect.provide(mockLayer(500, { error: "internal error" })),
      ),
    );
    expect(exit._tag).toBe("Failure");
  });
});
