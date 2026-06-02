// Thin fetch wrapper. Adds Accept: application/json so the dual-mode
// handlers return JSON; surfaces non-2xx as thrown errors carrying the
// status code so the caller can branch.

export class APIError extends Error {
  // detail is the server-provided message (parsed from {error: "…"}
  // bodies when available); body is the raw response text kept around
  // for debugging panels that want to show it verbatim.
  detail: string;
  constructor(public status: number, public body: string) {
    const detail = extractErrorDetail(body) || body;
    super(detail);
    this.detail = detail;
  }
}

// extractErrorDetail unwraps the conventional `{"error": "…"}` JSON
// envelope wick handlers return on non-2xx. Falls back to the raw body
// when it isn't JSON (or doesn't carry an `error` string), so callers
// always get something legible.
function extractErrorDetail(body: string): string {
  if (!body) return "";
  const trimmed = body.trim();
  if (!trimmed.startsWith("{")) return trimmed;
  try {
    const obj = JSON.parse(trimmed) as Record<string, unknown>;
    if (typeof obj.error === "string") return obj.error;
    if (typeof obj.message === "string") return obj.message;
  } catch {
    /* not JSON — drop through */
  }
  return trimmed;
}

export async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(path, {
    headers: { Accept: "application/json" },
    credentials: "same-origin",
  });
  if (!res.ok) throw new APIError(res.status, await res.text());
  return (await res.json()) as T;
}

export async function apiPost<T>(
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new APIError(res.status, await res.text());
  return (await res.json()) as T;
}

export async function apiDelete<T>(path: string): Promise<T> {
  const res = await fetch(path, {
    method: "DELETE",
    headers: { Accept: "application/json" },
    credentials: "same-origin",
  });
  if (!res.ok) throw new APIError(res.status, await res.text());
  return (await res.json()) as T;
}
