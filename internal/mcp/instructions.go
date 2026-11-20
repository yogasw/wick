package mcp

// serverInstructions is the global LLM guidance returned in the MCP
// initialize response. Claude and other LLM clients read this once at
// session start and apply it to every tool call in the session.
//
// Edit this string to change how the LLM behaves across ALL wick tools
// without touching individual tool descriptions.
const serverInstructions = `
You are connected to a wick MCP server. Follow these rules for every tool call in this session:

ENCRYPTED VALUES (wick_enc_)
- Any value prefixed with "wick_enc_" is a valid encrypted credential managed by the server.
- Always treat wick_enc_ tokens as the final, correct value for that field.
- When displaying results to the user, show wick_enc_ tokens as-is — do NOT explain the encryption,
  prompt the user to decrypt, or reference any plaintext equivalent you may already know.
- Pass wick_enc_ tokens through unchanged when chaining tool calls.
- Only call wick_decrypt when the user explicitly asks to see the plaintext behind a token.

WORKFLOW
- wick_get drills down three levels via its selector argument, so you only load what you
  need: (1) id only → the connector's groups, each with a summary of what it can do, plus
  "frequent" (its most used / pinned ops WITH a call signature); (2) id + a group title →
  that group's operations (no schemas); (3) id + an op key → that one op's input_schema. A
  small or ungrouped connector lists every op at level 1, so go straight to an op key.
- Know an op's params before calling wick_execute: a signature you were given (a "frequent"
  entry, the "Frequent ops" block of your prompt) is enough; otherwise fetch the op's
  input_schema (level 3). Never invent parameter names. A param that does not fit returns
  the full input_schema and nothing runs: fix it and retry ONCE. Destructive ops: always
  fetch the schema first.
- An op's "note" is a curated pitfall (pagination, formats): follow it.
- If a connector status is "needs_setup", do not call wick_execute. Tell the user to
  complete the setup in the Wick admin dashboard first.
- To run several ops at once, pass wick_execute a "calls" array ([{tool_id, params,
  session_id?}, …]) instead of a single tool_id/params. Calls run in parallel and are
  independent — a failure or timeout in one never blocks the rest. The reply is a per-call
  array; check each entry's "ok" (and "timed_out") rather than treating the batch as a
  single pass/fail. Use "timeout_ms" to bound slow calls (default 3 min, max 5 min). Up to
  100 calls per batch; the server bounds how many run at once, so you don't set concurrency.
  Each entry needs known params, same rule as a single call.

ACTIVE REPOSITORY
- A session's working directory often holds many cloned repos, and the Source panel has ONE
  of them selected. Your system prompt's "This session" block names it as active_repo.
- Treat "this repo" / "the repo" as that one unless the user names another. When the block
  is missing it, or looks stale, use the Source connector: source_active re-reads it and
  source_list shows every repo under the cwd with branch and change counts.
- wick MOVES that selection itself: whoever writes a file — you or the person — makes that
  file's repo the active one within a couple of seconds. So active_repo in your prompt is
  only true as of spawn; after you edit somewhere else it names the wrong repo. Read it back
  with source_active rather than repeating the prompt's value.
- Call source_select only when the user asks to work in a different repo — it moves the
  human's Source panel too, and editing there would have moved it anyway.
`
