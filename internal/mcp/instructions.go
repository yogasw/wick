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
  need: (1) id only → lists categories; (2) id + a category title → lists that category's
  operations (no schemas); (3) id + an op key → returns that one op's input_schema. An
  ungrouped connector skips level 1 (it lists ops directly), so go straight to an op key.
- Before calling wick_execute, always fetch the op's input_schema (level 3 — wick_get with
  the op key). Never guess parameters — derive them from that schema.
- If a connector status is "needs_setup", do not call wick_execute. Tell the user to
  complete the setup in the Wick admin dashboard first.
- To run several ops at once, pass wick_execute a "calls" array ([{tool_id, params,
  session_id?}, …]) instead of a single tool_id/params. Calls run in parallel and are
  independent — a failure or timeout in one never blocks the rest. The reply is a per-call
  array; check each entry's "ok" (and "timed_out") rather than treating the batch as a
  single pass/fail. Use "timeout_ms" to bound slow calls (default 3 min, max 5 min). Up to
  100 calls per batch; the server bounds how many run at once, so you don't set concurrency.
  Still fetch each op's input_schema first.
`
