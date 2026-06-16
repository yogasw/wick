import { HttpClientRequest } from "@effect/platform";
import { Effect } from "effect";
import { HttpClient } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";

type SendPayload = { text: string; files?: File[] };
type SendResult = { status: string };

function toAPIError(e: unknown): APIError {
  if (e instanceof APIError) return e;
  const err = e as { message?: string };
  return new APIError(0, err?.message ?? String(e));
}

export const sendMessage = (
  base: string,
  id: string,
  payload: SendPayload,
): Effect.Effect<SendResult, APIError, HttpClient.HttpClient> => {
  const url = `${base}/sessions/${encodeURIComponent(id)}/send`;
  const { text, files } = payload;

  if (files && files.length > 0) {
    const fd = new FormData();
    fd.append("text", text);
    files.forEach((f) => fd.append("files", f, f.name));

    return Effect.scoped(
      Effect.gen(function* () {
        const client = yield* HttpClient.HttpClient;
        const req = HttpClientRequest.post(url).pipe(
          HttpClientRequest.bodyFormData(fd),
        );
        const response = yield* client.execute(req);
        if (response.status < 200 || response.status >= 300) {
          const body = yield* response.text.pipe(Effect.orElseSucceed(() => ""));
          return yield* Effect.fail(new APIError(response.status, body));
        }
        return (yield* response.json) as SendResult;
      }),
    ).pipe(Effect.mapError(toAPIError));
  }

  return Effect.scoped(
    Effect.gen(function* () {
      const client = yield* HttpClient.HttpClient;
      const req = yield* HttpClientRequest.post(url).pipe(
        HttpClientRequest.bodyJson({ text }),
      );
      const response = yield* client.execute(req);
      if (response.status < 200 || response.status >= 300) {
        const body = yield* response.text.pipe(Effect.orElseSucceed(() => ""));
        return yield* Effect.fail(new APIError(response.status, body));
      }
      return (yield* response.json) as SendResult;
    }),
  ).pipe(Effect.mapError(toAPIError));
};
