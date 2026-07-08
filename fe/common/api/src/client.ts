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

/**
 * apiPostSSE POSTs to a Server-Sent Events endpoint and invokes onEvent for
 * each `data:` frame (parsed as JSON). EventSource can't POST, so this uses
 * fetch + a stream reader. Resolves when the stream ends; rejects with APIError
 * on a non-2xx response. Pass a signal to cancel (abort closes the stream).
 *
 * The endpoint is expected to also accept a plain POST (no Accept header) and
 * return JSON — callers can fall back to apiPost for non-streaming use.
 */
export async function apiPostSSE<E = unknown>(
  path: string,
  onEvent: (data: E) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch(path, {
    method: "POST",
    headers: { Accept: "text/event-stream" },
    credentials: "same-origin",
    signal,
  });
  if (!resp.ok) {
    const body = await resp.text().catch(() => "");
    throw new APIError(resp.status, body);
  }
  if (!resp.body) return;
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    // SSE frames are separated by a blank line. Process each complete frame.
    let sep: number;
    while ((sep = buf.indexOf("\n\n")) !== -1) {
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      const line = frame.split("\n").find((l) => l.startsWith("data:"));
      if (!line) continue;
      const json = line.slice("data:".length).trim();
      if (!json) continue;
      try {
        onEvent(JSON.parse(json) as E);
      } catch {
        /* skip malformed frame */
      }
    }
  }
}
