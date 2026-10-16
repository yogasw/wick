import { HttpClientRequest } from "@effect/platform";
import { Effect } from "effect";
import { HttpClient } from "@effect/platform";
import { APIError } from "@wick-fe/common-api";

/** reply_to: turn id of the bubble being answered; the server checks it
    and builds the quote itself. */
type SendPayload = { text: string; files?: File[]; reply_to?: string };
/** turn_id: the id the server stored the message under. */
type SendResult = { status: string; turn_id?: string };

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
  const { text, files, reply_to } = payload;

  if (files && files.length > 0) {
    const fd = new FormData();
    fd.append("text", text);
    if (reply_to) fd.append("reply_to", reply_to);
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
        HttpClientRequest.bodyJson(reply_to ? { text, reply_to } : { text }),
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
