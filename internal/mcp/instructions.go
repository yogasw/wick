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
- Before calling wick_execute, always call wick_get to read the tool's input_schema.
- Never guess parameters — derive them from the schema returned by wick_get.
- If a connector status is "needs_setup", do not call wick_execute. Tell the user to
  complete the setup in the Wick admin dashboard first.
`
