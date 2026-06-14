import {
  FetchHttpClient,
  HttpClient,
  HttpClientRequest,
  HttpClientResponse,
} from "@effect/platform";
import { Cause, Effect, Exit, Layer, Option, Scope } from "effect";

export class APIError extends Error {
  detail: string;
  constructor(public readonly status: number, public readonly body: string) {
    const detail = extractDetail(body) || body;
    super(detail);
    this.detail = detail;
  }
}

function extractDetail(body: string): string {
  if (!body) return "";
  const t = body.trim();
  if (!t.startsWith("{")) return t;
  try {
    const parsed = JSON.parse(t) as Record<string, unknown>;
    if (typeof parsed.error === "string") return parsed.error;
    if (typeof parsed.message === "string") return parsed.message;
  } catch { /* not JSON */ }
  return t;
}

function toAPIError(e: unknown): APIError {
  if (e instanceof APIError) return e;
  const err = e as { message?: string };
  return new APIError(0, err?.message ?? String(e));
}

export const WickClientLayer = FetchHttpClient.layer.pipe(
  Layer.provide(
    Layer.succeed(FetchHttpClient.RequestInit, { credentials: "same-origin" }),
  ),
);

function send<T>(
  exec: (
    client: HttpClient.HttpClient,
  ) => Effect.Effect<HttpClientResponse.HttpClientResponse, unknown, Scope.Scope>,
): Effect.Effect<T, APIError, HttpClient.HttpClient> {
  return Effect.scoped(
    Effect.gen(function* () {
      const client = (yield* HttpClient.HttpClient).pipe(
        HttpClient.mapRequest(HttpClientRequest.setHeader("Accept", "application/json")),
      );
      const response = yield* exec(client);
      if (response.status < 200 || response.status >= 300) {
        const body = yield* response.text.pipe(Effect.orElseSucceed(() => ""));
        return yield* Effect.fail(new APIError(response.status, body));
      }
      return (yield* response.json) as T;
    }),
  ).pipe(Effect.mapError(toAPIError));
}

export const apiGetE = <T>(path: string): Effect.Effect<T, APIError, HttpClient.HttpClient> =>
  send<T>((client) => client.get(path));

export const apiDeleteE = <T>(path: string): Effect.Effect<T, APIError, HttpClient.HttpClient> =>
  send<T>((client) => client.del(path));

export const apiPostE = <T>(
  path: string,
  body?: unknown,
): Effect.Effect<T, APIError, HttpClient.HttpClient> =>
  send<T>((client) =>
    body === undefined
      ? client.post(path)
      : HttpClientRequest.post(path).pipe(
          HttpClientRequest.bodyJson(body),
          Effect.flatMap((req) => client.execute(req)),
        ),
  );

export function runPromiseUnwrapped<T>(
  effect: Effect.Effect<T, APIError, never>,
  options?: { readonly signal?: AbortSignal },
): Promise<T> {
  return Effect.runPromiseExit(effect, options).then((exit) => {
    if (Exit.isSuccess(exit)) return exit.value;
    const failure = Cause.failureOption(exit.cause);
    if (Option.isSome(failure)) throw failure.value;
    const squashed = Cause.squash(exit.cause);
    throw squashed instanceof Error ? squashed : new APIError(0, String(squashed));
  });
}

export function apiGet<T>(path: string): Promise<T> {
  return runPromiseUnwrapped(apiGetE<T>(path).pipe(Effect.provide(WickClientLayer)));
}

export function apiPost<T>(path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return runPromiseUnwrapped(
    apiPostE<T>(path, body).pipe(Effect.provide(WickClientLayer)),
    signal ? { signal } : undefined,
  );
}

export function apiDelete<T>(path: string): Promise<T> {
  return runPromiseUnwrapped(apiDeleteE<T>(path).pipe(Effect.provide(WickClientLayer)));
}
