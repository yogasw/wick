# Plan: Manual Context Compaction for Codex Sessions

## Objective

Enable Wick users to run `/compact` on Codex-backed sessions and perform real
Codex history compaction, instead of forwarding `/compact` as an ordinary model
prompt.

## Verified behavior

### `codex exec resume <thread-id> /compact` does not work

`codex exec` has no slash-command handling. The model may reply with
`Context compacted.`, but the stored context is not compacted.

Observed result:

| Stage | Context level |
| --- | ---: |
| Before `/compact` | 14,359 tokens |
| After `/compact` through `codex exec resume` | 17,685 tokens |

The context increased by 3,326 tokens.

### Codex App Server compaction works

Codex App Server exposes the stable request:

```json
{
  "method": "thread/compact/start",
  "id": 25,
  "params": {
    "threadId": "<codex-thread-id>"
  }
}
```

Successful execution emits:

```text
item/started   type=contextCompaction
item/completed type=contextCompaction
```

Observed result:

| Stage | Context level |
| --- | ---: |
| Before App Server compaction | 17,727 tokens |
| Compacted conversation body | 4,925 tokens |

A second test grew the thread through three more turns, compacted it again,
and successfully recalled facts from all three pre-compaction turns.

## Important token-accounting detail

The token count emitted during `contextCompaction` represents the compacted
conversation body. It does not include the complete static prefix that Codex
adds to the next request.

Example from the second test:

| Stage | Tokens |
| --- | ---: |
| Last request before compaction | 16,035 total |
| Compacted body reported by App Server | 4,944 body |
| First request after compaction | 15,753 total |

The next request again includes system instructions, developer instructions,
skills, tools, and other static prefix content. Wick must not present the body
count as the next request's final total context level.

## Proposed design

### 1. Add a Codex App Server client

Create a small internal JSON-RPC client that can:

1. Start `codex app-server --stdio` with the same environment as the Codex
   session, especially `CODEX_HOME`.
2. Send `initialize` and `initialized`.
3. Resume the existing Codex thread with `thread/resume`.
4. Send `thread/compact/start`.
5. Consume notifications until compaction completes or fails.
6. Shut down the temporary App Server process cleanly.

Keep this client narrowly scoped to compaction initially. It does not need to
replace Wick's existing `codex exec` turn execution.

### 2. Preserve provider configuration

The App Server process must receive the same effective configuration as normal
Codex turns:

- binary path;
- `CODEX_HOME`;
- model/provider configuration overrides;
- authentication environment;
- working directory;
- session-scoped environment values.

Never print authentication values in logs or user-visible errors.

### 3. Route bare `/compact` to the App Server

When `Pool.send` receives an exact bare `/compact` for a Codex session:

1. Do not append it as a user message.
2. Do not pass it to `codex exec resume`.
3. Confirm that the session is idle.
4. Resolve the persisted Codex thread ID.
5. Run the App Server compaction flow.

Messages such as `/compact please` remain ordinary user messages.

### 4. Handle concurrency safely

Compaction must not race with a normal Codex turn.

- Reject or queue compaction while a turn is active.
- Hold the same per-session execution lock used for Codex respawns.
- Prevent duplicate `/compact` requests while compaction is running.
- Release the lock on success, failure, cancellation, or timeout.

### 5. Treat completion events as authoritative

Success requires all of the following:

1. `thread/compact/start` returns a successful response.
2. `item/started` reports `type=contextCompaction`.
3. `item/completed` reports the same compaction item.
4. The compaction turn reaches `turn/completed` without an error.

Do not infer success from an agent text response.

### 6. Surface compaction in Wick

On success, append a Wick system turn with:

- `kind: "compaction"`;
- trigger: `manual`;
- pre-compaction token level;
- compacted body token count when available;
- Codex thread and turn identifiers in internal metadata only.

Suggested UI text:

```text
Compacted conversation body 16.0K → 4.9K · manual
```

Avoid claiming that the next complete model request will contain only 4.9K
tokens.

### 7. Refresh context usage after compaction

Immediately store the compacted-body reading for the compaction event. On the
next normal Codex turn, replace the context meter with the regular
`last_token_usage` and `model_context_window` reading from the rollout journal.

This keeps both values truthful:

- compaction event: summarized conversation-body size;
- context meter: complete context level of the latest real model request.

### 8. Update capability gating

Change Codex manual-compaction capability from unavailable to available only
when:

- the configured Codex binary exposes App Server support; and
- a usable Codex thread ID exists.

Prefer a runtime capability probe over a version-number comparison. If the
method is unavailable, preserve the existing explanation and automatic
compaction fallback.

### 9. Failure behavior

Return a clear, non-destructive error when:

- no Codex thread ID is stored;
- `thread/resume` cannot load the thread;
- the App Server method is unavailable;
- compaction is requested during an active turn;
- the process exits before `item/completed`;
- the request times out;
- Codex reports an error notification.

Do not append a successful compaction system turn on any partial failure. The
original thread must remain resumable through the normal `codex exec` path.

## Implementation areas

Likely files and packages:

- `internal/agents/provider/compact_capability.go`
- `internal/agents/provider/codex/`
- `internal/agents/pool/pool.go`
- Codex event and rollout token readers
- conversation API context capability response
- compaction system-turn rendering and tests

Exact placement should follow existing provider and process-lifecycle
abstractions rather than adding App Server behavior directly to HTTP handlers.

## Test plan

### Unit tests

- Parse App Server JSON-RPC responses and notifications.
- Require matching `contextCompaction` start/completion events.
- Reject completion events for another thread or item.
- Handle JSON-RPC errors, malformed output, EOF, cancellation, and timeout.
- Verify bare `/compact` interception.
- Verify `/compact please` remains an ordinary message.
- Verify secrets are redacted from errors and logs.
- Verify Codex capability is false when App Server is unavailable.

### Provider integration tests

Use a fake App Server process to verify:

- initialize → resume → compact request ordering;
- correct `CODEX_HOME`, cwd, and thread ID;
- successful system-turn creation;
- no system turn on partial failure;
- process cleanup;
- duplicate-request prevention;
- active-turn locking.

### Live acceptance test

1. Start a fresh Codex-backed Wick session.
2. Send several turns with facts that can be checked later.
3. Record `last_token_usage.input_tokens` and `model_context_window`.
4. Run `/compact`.
5. Confirm App Server emits `contextCompaction` start and completion.
6. Confirm the compacted-body token count decreases.
7. Send another turn asking for the earlier facts.
8. Confirm the facts remain available.
9. Confirm the context meter updates to the next real request's total level.
10. Resume the same session again to verify persistence across process restarts.

## Rollout

1. Land the App Server client and unit tests.
2. Add Codex `/compact` routing behind a feature flag if desired.
3. Run live tests on the oldest supported Codex CLI and the current release.
4. Enable `CanCompact` for Codex after capability probing is reliable.
5. Update provider documentation and changelog.

## Definition of done

- Bare `/compact` never reaches the Codex model as ordinary text.
- Wick invokes `thread/compact/start` for supported Codex sessions.
- Success is based on App Server lifecycle events.
- The conversation remains resumable after compaction.
- Pre-compaction facts survive in summarized form.
- The UI distinguishes compacted-body size from full next-request context.
- Failure leaves the session intact and reports an actionable error.
- Unit, integration, and live acceptance tests pass.
