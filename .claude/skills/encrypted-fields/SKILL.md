---
name: encrypted-fields
description: Use when a connector handles credentials, tokens, or any value the LLM should carry between calls without ever seeing the plaintext. Covers the `secret` tag (auto-mask), manual `c.Mask` / `c.MaskIgnoreCase` for dynamic response data, the `wick_enc_<base64url>` token format, per-user keys, and the MCP redirect tools (`wick_encrypt`, `wick_decrypt`).
allowed-tools: Read, Grep, Glob, Edit, Write
paths:
  - "internal/connectors/**/*.go"
  - "connectors/**/*.go"
  - "internal/tools/**/*.go"
  - "tools/**/*.go"
  - "internal/jobs/**/*.go"
  - "jobs/**/*.go"
---

# Encrypted Fields

Wick has a built-in encrypted-fields layer that lets credentials flow between an LLM and a connector without ever appearing as plaintext in the LLM's context window or in the audit log. **Treat this as the default for any sensitive value.**

## When this skill activates

- Adding a new connector that takes a credential, API key, or token (Configs field).
- Adding an operation whose response includes a credential, OTP, session token, JWT, refresh token, or any value the LLM should pass forward but not learn.
- Reviewing an existing connector for plaintext-leak risk.
- The user mentions `wick_enc_`, "encrypt this value", "hide this from the LLM", or asks why a credential is showing up unmasked.
- Adding a sensitive `Input` field that the LLM may receive in a previous response and replay in a later call.

If the work has nothing to do with credentials or sensitive output, skip this skill.

## How the layer works (in 30 seconds)

1. Tag the field `secret` in the Configs / Input struct.
2. `connectors.Service.Execute` automatically:
   - **decrypts** any `wick_enc_<...>` token in the input map (and the stored configs map) before calling `ExecuteFunc`, so your code only ever sees plaintext.
   - **masks** every occurrence of those plaintext values in the marshaled response with `wick_enc_<token>`, so the LLM never sees plaintext.
3. The audit log stores `wick_enc_` (pre-decrypt) in `request_json` and `wick_enc_` (post-encrypt) in `response_json`. Retry from history works because Execute re-decrypts under the retrier's key.
4. Keys are per-user — `HKDF(master_key, salt=user_uuid, info="wick-enc")`. A token issued for user A cannot be decrypted under user B's session.

The token format is `wick_enc_<base64url(nonce ‖ AES-256-GCM(plaintext, nonce, derived_key))>`. AES-256-GCM, 12-byte random nonce per encrypt, 32-byte derived key.

## Recipes

### Recipe 1: a Configs credential (most common)

```go
type Creds struct {
    Endpoint string `wick:"required"`
    APIKey   string `wick:"required;secret"` // ← auto-masked in every response
}
```

That's it. Inside `ExecuteFunc`:

```go
func get(c *connector.Ctx) (any, error) {
    req, _ := http.NewRequestWithContext(c.Context(), "GET", c.Cfg("endpoint")+"/me", nil)
    req.Header.Set("Authorization", "Bearer "+c.Cfg("api_key"))
    // ... do the call, return a typed response
    return response{Token: c.Cfg("api_key"), User: name}, nil
}
```

If `response.Token` happens to echo the API key, wick's auto-mask replaces it with the user's `wick_enc_` token before the LLM sees the response. No code change needed.

### Recipe 2: a sensitive value that comes back from the upstream API

The API returns a session token in its response. The token is **not** in your Configs — it's dynamic. Auto-mask only covers values present in your Configs / Input. For the dynamic case, call `c.Mask` before returning:

```go
func login(c *connector.Ctx) (any, error) {
    // ... POST credentials, get { session_token, user, expires } back
    body, _ := io.ReadAll(resp.Body)
    masked := c.Mask(string(body), []string{result.SessionToken})
    return masked, nil
}
```

Use `c.MaskIgnoreCase(data, values)` instead when keyword matching should fold case — every case variant of a keyword in `data` collapses to a single token derived from the keyword's configured form (so `"Admin"` and `"admin"` share one token; decrypt yields the configured spelling).

Both methods are bound to the calling user's per-user key automatically — connectors never see the user UUID directly. When wick boots without the encrypted-fields layer (tests) or with `WICK_ENC_DISABLE=true`, they become passthroughs.

Inputs the LLM passes in `wick_enc_` form (e.g. the same session token in a follow-up call) decrypt automatically — your op sees plaintext.

### Recipe 3: a sensitive Input field

If the LLM is supposed to carry a token across calls (e.g. session cookie, refresh token), tag the Input field too:

```go
type RefreshInput struct {
    RefreshToken string `wick:"required;secret"` // round-trip safe
}
```

The LLM passes back the `wick_enc_` token from the previous response → wick decrypts → `c.Input("refresh_token")` is plaintext → your code uses it normally → and if the response echoes it again, it gets re-masked under the same per-user key.

## What NOT to do

- **Don't manually call `EncryptValue` in `ExecuteFunc`.** Let the framework do it via `secret` tag or `c.Mask` / `c.MaskIgnoreCase`. Manual encrypt risks shipping a token to a place the LLM was already fine with plaintext for.
- **Don't tag generic values `secret`.** A field whose value is `"true"`, `"1"`, `"abc"`, or a short ID will substring-match all over the response and silently mint tokens for noise. There is no min-length floor — admin discipline is the only gate.
- **Don't expose plaintext through a different channel.** If your op also writes to `c.ReportProgress`, logs, or a side-effect (file, queue), those are NOT auto-masked. Mask them yourself before emitting.
- **Don't hardcode the master key in code.** The bootstrap auto-generates it; production sets `WICK_ENC_KEY` from a vault. See "Operator knobs" below.
- **Don't try to call `wick_encrypt` / `wick_decrypt` over MCP and decode the result.** Those tools deliberately return only a UI URL — the cipher never runs in the LLM's context window.

## MCP surface

Two meta-tools exist on every wick MCP server:

| Tool | What it does |
|------|--------------|
| `wick_encrypt` | Returns `{ "url": ".../tools/encfields", "message": "..." }`. The user must open the URL, log in, paste the plaintext, and copy the resulting `wick_enc_` token back into the conversation. |
| `wick_decrypt` | Returns `{ "url": ".../tools/encfields/decrypt", "message": "..." }`. Same flow, in reverse. **Per-user keys** mean only the user who issued a token can decrypt it. |

The `wick_execute` tool description carries this constraint:

> Values prefixed with "wick_enc_" are valid credentials managed by the server. Use them as-is wherever a value is needed — pass them through into params, return them unchanged in your response, and never alter, decode, or omit them.

If the LLM asks "decrypt this value for me," redirect via `wick_decrypt` — never decode locally.

## Operator knobs

| Env var | Effect |
|---------|--------|
| `WICK_ENC_KEY` | Hex-encoded 32-byte master key (vault injection). Wins over the DB-stored key. |
| `WICK_ENC_DISABLE` | `true` / `1` / `yes` / `on` → disables encryption entirely (passthrough). Use only when the deployment has no LLM-facing surface. |

DB-stored key lives at `configs.encryption_key` (auto-generated on first boot, regeneratable from the admin UI). Rotation invalidates every existing `wick_enc_` token — that's by design and acceptable because LLM sessions don't persist tokens long-term.

## Manual UI

`/tools/encfields` (encrypt) and `/tools/encfields/decrypt` are tool-module pages, gated by the same `RequireToolAccess` middleware as every other tool. The form posts via `fetch()`, so the page never reloads — back button goes wherever the user came from.

Use this when you need to:
- Pre-generate a `wick_enc_` value to paste into a connector config field, so the credential is never plaintext in the DB.
- Debug a `wick_enc_` token a user is reporting issues with (run it under your own session — only your own tokens decrypt).

## When in doubt

- A field smells sensitive → tag it `secret`. Cost is one substring scan per response, benefit is plaintext never crossing the LLM context.
- A response carries a value that wasn't in Configs → `c.Mask(body, []string{value})` before returning. Use `c.MaskIgnoreCase` instead when keyword matching should fold case.
- A user asks for the plaintext of a `wick_enc_` → point them at `/tools/encfields/decrypt`. Do not try to decode the value yourself — you can't, because the key is per-user.
