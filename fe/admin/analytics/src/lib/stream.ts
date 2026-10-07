import type { AnalyticsResponse, AnalyticsStreamLine } from "$lib/types";

/** Reads the NDJSON analytics stream, reporting progress as it goes.
 *
 *  The page's slow part is the server reading several hundred session
 *  files. A spinner that cannot say how far along it is looks identical to
 *  a page that has hung, so the server counts the files first and streams
 *  its position; this turns that into a real progress bar.
 *
 *  Falls back to a plain JSON fetch when streaming is unavailable — an old
 *  browser, or a proxy that buffers the response — because a page that
 *  loads without progress still beats a page that does not load. */
export async function loadAnalytics(
  endpoint: string,
  opts: {
    /** A day count, or "all" for everything back to the oldest conversation. */
    days?: number | "all";
    /** A custom range, YYYY-MM-DD. Wins over days when both are given. */
    from?: string;
    to?: string;
    /** Channels to include. Empty = every channel. */
    channels?: string[];
    /** Specific bots to include ("slack:<owner id>"). Empty = all of them. */
    instances?: string[];
    onProgress?: (done: number, total: number) => void;
    fetchImpl?: typeof fetch;
    /** How long the stream may say nothing before it counts as stalled. */
    stallMs?: number;
  } = {},
): Promise<AnalyticsResponse> {
  const f = opts.fetchImpl ?? fetch;
  const url = new URL(endpoint, location.href);
  if (opts.days) url.searchParams.set("days", String(opts.days));  // "all" is a value the server knows
  if (opts.from) url.searchParams.set("from", opts.from);
  if (opts.to) url.searchParams.set("to", opts.to);
  if (opts.channels?.length) url.searchParams.set("channels", opts.channels.join(","));
  if (opts.instances?.length) url.searchParams.set("instances", opts.instances.join(","));

  const streamURL = new URL(url);
  streamURL.searchParams.set("stream", "1");

  // A stalled connection is silent, and silence here used to be
  // indistinguishable from work: the progress bar sat at its last count
  // forever — "2311 / 2311" with the server long since finished — because the
  // read loop had nothing to time out against. One retry turns that into
  // either an answer or an error somebody can read.
  try {
    return await readStream(f, streamURL, url, opts);
  } catch (e) {
    if (e instanceof StreamStalled) return readStream(f, streamURL, url, opts);
    throw e;
  }
}

/** Thrown when the stream goes quiet for longer than a live one ever would. */
class StreamStalled extends Error {
  constructor(ms: number) {
    super(`the connection went quiet for ${Math.round(ms / 1000)}s`);
    this.name = "StreamStalled";
  }
}

/** Rejects with StreamStalled — and drops the connection — when `p` has said
 *  nothing for `ms`. The server writes a progress line every ~120ms, so any
 *  silence this long is a dead socket, not a slow page. */
function beforeSilence<T>(p: Promise<T>, ms: number, onStall: () => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => {
      onStall();
      reject(new StreamStalled(ms));
    }, ms);
    p.then(
      (v) => {
        clearTimeout(timer);
        resolve(v);
      },
      (e) => {
        clearTimeout(timer);
        reject(e);
      },
    );
  });
}

async function readStream(
  f: typeof fetch,
  streamURL: URL,
  plainURL: URL,
  opts: { onProgress?: (done: number, total: number) => void; stallMs?: number },
): Promise<AnalyticsResponse> {
  const stallMs = opts.stallMs ?? 20_000;
  const ctrl = new AbortController();
  const res = await beforeSilence(
    f(streamURL.toString(), {
      headers: { Accept: "application/x-ndjson" },
      cache: "no-store",
      signal: ctrl.signal,
    }),
    stallMs,
    () => ctrl.abort(),
  );
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  if (!res.body) return plain(f, plainURL);

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let result: AnalyticsResponse | null = null;

  for (;;) {
    const { done, value } = await beforeSilence(reader.read(), stallMs, () => ctrl.abort());
    if (value) buf += decoder.decode(value, { stream: true });
    // A chunk can split a line anywhere, so only whole lines are parsed
    // and the tail is kept for the next read.
    let nl: number;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (!line) continue;
      const msg = JSON.parse(line) as AnalyticsStreamLine;
      if (msg.type === "progress") opts.onProgress?.(msg.done, msg.total);
      else if (msg.type === "error") throw new Error(msg.error);
      else if (msg.type === "result") result = msg.data;
    }
    if (done) break;
  }
  if (!result) throw new Error("the server closed the stream without sending the data");
  return result;
}

async function plain(f: typeof fetch, url: URL): Promise<AnalyticsResponse> {
  const res = await f(url.toString(), { headers: { Accept: "application/json" }, cache: "no-store" });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return (await res.json()) as AnalyticsResponse;
}
