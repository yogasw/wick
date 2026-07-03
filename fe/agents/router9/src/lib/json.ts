// prettyJSON pretty-prints a JSON string with 2-space indent. Non-JSON
// input (e.g. an SSE stream or plain text) is returned unchanged so the
// caller can still show it verbatim. Empty in → empty out.
export function prettyJSON(raw: string): string {
  const t = (raw ?? "").trim();
  if (!t) return "";
  try {
    return JSON.stringify(JSON.parse(t), null, 2);
  } catch {
    return raw;
  }
}
