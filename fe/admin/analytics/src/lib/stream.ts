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
    onProgress?: (done: number, total: number) => void;
    fetchImpl?: typeof fetch;
  } = {},
): Promise<AnalyticsResponse> {
  const f = opts.fetchImpl ?? fetch;
  const url = new URL(endpoint, location.href);
  if (opts.days) url.searchParams.set("days", String(opts.days));  // "all" is a value the server knows
  if (opts.from) url.searchParams.set("from", opts.from);
  if (opts.to) url.searchParams.set("to", opts.to);
  if (opts.channels?.length) url.searchParams.set("channels", opts.channels.join(","));

  const streamURL = new URL(url);
  streamURL.searchParams.set("stream", "1");

  const res = await f(streamURL.toString(), {
    headers: { Accept: "application/x-ndjson" },
    cache: "no-store",
  });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  if (!res.body) return plain(f, url);

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let result: AnalyticsResponse | null = null;

  for (;;) {
    const { done, value } = await reader.read();
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
