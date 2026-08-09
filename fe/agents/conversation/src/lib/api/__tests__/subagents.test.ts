import { describe, test, expect } from "vitest";
import { Effect, Layer } from "effect";
import { HttpClient, HttpClientRequest, HttpClientResponse } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";
import { continueSubAgent } from "../subagents.js";

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

describe("continueSubAgent", () => {
  test("posts the task to the delegation's continue endpoint", async () => {
    let captured: HttpClientRequest.HttpClientRequest | null = null;
    const capturing = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        captured = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(
            req,
            new Response(JSON.stringify({ delegation_id: "d1", status: "running", resumed: true }), {
              status: 200,
              headers: { "Content-Type": "application/json" },
            }),
          ),
        );
      }),
    );

    const out = await Effect.runPromise(
      continueSubAgent("", "d1", "keep going").pipe(Effect.provide(capturing)),
    );

    expect(out.resumed).toBe(true);
    const req = captured as unknown as HttpClientRequest.HttpClientRequest;
    expect(req.method).toBe("POST");
    expect(req.url).toContain("/api/delegations/d1/continue");
  });

  test("url-encodes the delegation id", async () => {
    let captured: HttpClientRequest.HttpClientRequest | null = null;
    const capturing = Layer.succeed(
      HttpClient.HttpClient,
      HttpClient.make((req) => {
        captured = req;
        return Effect.succeed(
          HttpClientResponse.fromWeb(req, new Response("{}", { status: 200 })),
        );
      }),
    );

    await Effect.runPromise(
      continueSubAgent("", "a/b", "go").pipe(Effect.provide(capturing)),
    );

    const req = captured as unknown as HttpClientRequest.HttpClientRequest;
    expect(req.url).toContain("a%2Fb");
  });

  // resumed:false means the sub-agent woke in its old session with no
  // memory of its own work. The caller has to be able to see that, or it
  // shows a success that quietly misrepresents what happened.
  test("surfaces resumed:false rather than flattening it into success", async () => {
    const out = await Effect.runPromise(
      continueSubAgent("", "d1", "go").pipe(
        Effect.provide(
          mockLayer(200, {
            delegation_id: "d1",
            status: "running",
            resumed: false,
            note: "does not remember its previous work",
          }),
        ),
      ),
    );
    expect(out.resumed).toBe(false);
    expect(out.note).toContain("does not remember");
  });

  // 409 means it started working again between the render and the click.
  // Unlike interrupt's 409 — where the work is genuinely done and the
  // result stands — here nothing happened, so it must NOT be mapped to a
  // success value: the user's instruction was never delivered.
  test("fails on 409 so a lost race is not reported as continued", async () => {
    const err = await Effect.runPromise(
      continueSubAgent("", "d1", "go").pipe(
        Effect.flip,
        Effect.provide(mockLayer(409, { error: "is still working" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(409);
  });

  test("fails on 403", async () => {
    const err = await Effect.runPromise(
      continueSubAgent("", "d1", "go").pipe(
        Effect.flip,
        Effect.provide(mockLayer(403, { error: "not allowed" })),
      ),
    );
    expect(err).toBeInstanceOf(APIError);
  });
});
