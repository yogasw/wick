/* Client-side check of the Watch tab's steps JSON. Only the shape the form
   can catch early — the server validates fully and its error (field path +
   accepted values) is shown as-is. */

export const WATCH_STEPS_EXAMPLE = `[
  {"name": "pipeline", "kind": "connector",
   "tool_id": "conn:<connector_id>/get_pipeline",
   "params": {"workspace": "<ws>", "repo_slug": "<repo>", "pipeline_uuid": "{<uuid>}"}},
  {"name": "done?", "kind": "check",
   "rules": [{"path": "state.name", "op": "equals", "value": "COMPLETED"}]}
]`;

const KINDS = ["connector", "check", "bash"];

export function parseWatchSteps(text: string): { steps: unknown[] } | { error: string } {
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch (e) {
    return { error: "steps: invalid JSON — " + (e instanceof Error ? e.message : String(e)) };
  }
  if (!Array.isArray(v)) return { error: "steps: must be a JSON array of steps" };
  if (v.length === 0) return { error: "steps: a watch needs at least one step" };
  if (v.length > 8) return { error: `steps: too many steps (${v.length}, max 8)` };
  for (let i = 0; i < v.length; i++) {
    const st = v[i] as { kind?: unknown } | null;
    if (!st || typeof st !== "object" || Array.isArray(st)) return { error: `steps[${i}]: must be an object` };
    const kind = typeof st.kind === "string" ? st.kind.trim().toLowerCase() : "";
    if (!KINDS.includes(kind)) return { error: `steps[${i}].kind: ${JSON.stringify(st.kind ?? "")} — use one of: ${KINDS.join(", ")}` };
  }
  return { steps: v };
}
