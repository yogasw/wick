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

/* A code step (Code Mode): the script gets input.snapshot (what it returned
   last fire) and returns {changed, snapshot, done?}. Shown as a hint, not
   pre-filled — inline code or a project-relative script_path, not both. */
export const WATCH_CODE_STEP_EXAMPLE = `{"name": "new rows", "kind": "code", "lang": "ts",
 "code": "const rows = await wick.call('conn:<id>/list_rows', {}); const seen = input.snapshot ?? []; return {changed: rows.filter(r => !seen.includes(r.id)), snapshot: rows.map(r => r.id)};"}`;

const KINDS = ["connector", "check", "bash", "code"];
const CODE_LANGS = ["ts", "js"];

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
    const st = v[i] as { kind?: unknown; code?: unknown; script_path?: unknown; lang?: unknown } | null;
    if (!st || typeof st !== "object" || Array.isArray(st)) return { error: `steps[${i}]: must be an object` };
    const kind = typeof st.kind === "string" ? st.kind.trim().toLowerCase() : "";
    if (!KINDS.includes(kind)) return { error: `steps[${i}].kind: ${JSON.stringify(st.kind ?? "")} — use one of: ${KINDS.join(", ")}` };
    if (kind === "code") {
      const err = codeStepError(st, `steps[${i}]`);
      if (err) return { error: err };
    }
  }
  return { steps: v };
}

const filled = (x: unknown) => typeof x === "string" && x.trim() !== "";

/* The server's code-step shape: exactly one of code / script_path, the path
   relative and inside the project folder, lang ts or js. */
function codeStepError(st: { code?: unknown; script_path?: unknown; lang?: unknown }, at: string): string {
  if (filled(st.code) === filled(st.script_path))
    return `${at}: kind=code takes exactly one of code (inline) or script_path (relative to the project folder)`;
  if (filled(st.script_path)) {
    const p = String(st.script_path).trim();
    if (p.startsWith("/") || p.startsWith("~")) return `${at}.script_path: must be relative to the project folder`;
    if (p.split(/[\\/]/).includes("..")) return `${at}.script_path: must stay inside the project folder (no ..)`;
  }
  const lang = typeof st.lang === "string" ? st.lang.trim().toLowerCase() : "";
  if (lang && !CODE_LANGS.includes(lang)) return `${at}.lang: ${JSON.stringify(st.lang)} — use one of: ${CODE_LANGS.join(", ")}`;
  return "";
}
