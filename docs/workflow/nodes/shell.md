---
outline: deep
---

# `shell`

Run a local shell command. Captures stdout / stderr / exit_code.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/shell.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/shell.go) |
| **When to use** | Operating on local files, running a CLI tool, escape hatch for anything wick hasn't typed yet. |
| **Gate** | Participates in [Command Gate](/guide/command-gate) policy when enabled — `PermissionMode` applies. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `command` | template | ✅ | The command line. Rendered as Go template. |
| `cwd` | path (template) | | Working directory. Default: workflow folder. |
| `env` | kvlist (template) | | Extra environment variables. Each value is rendered. |
| `parse_output` | dropdown | | `raw` (default) / `json` / `lines`. |
| `timeout_sec` | int | | Per-call timeout in seconds. |

## Output

| Field | Type | What |
|---|---|---|
| `stdout` | string | Captured stdout. |
| `stderr` | string | Captured stderr. |
| `exit_code` | int | Process exit code. |

When `parse_output: json` and stdout is valid JSON, the parsed value is also merged into `.Node.<id>.*` (so `{"foo": "bar"}` exposes `.Node.<id>.foo`). `parse_output: lines` splits stdout on `\n` and stores the array under `.Node.<id>.lines`.

## Example

```yaml
- id: count_files
  type: shell
  command: 'find {{.Env.PROJECT_DIR}} -name "*.go" | wc -l'
  parse_output: raw
```

## Pair with

- [`go_script`](./go-script) — pure Go alternative when you don't need to spawn a process.
- [`transform`](./transform) — reshape the captured output before passing downstream.
- [`http`](./http) — for HTTP-only side-effects, prefer this over `curl`.
