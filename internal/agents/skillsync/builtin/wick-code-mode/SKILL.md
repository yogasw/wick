---
name: wick-code-mode
description: Use when a task needs many connector calls, a loop, dependent calls or a big result to filter — write a wick_run_code script (script_path, wick.file in codemode/, watch and workflow code steps).
---

# Code Mode (wick_run_code)

A short TypeScript/JavaScript script runs in a sandbox (the `codemode_goja`
runner plugin) and calls connectors through wick. Raw connector output stays
inside the script; only its return value comes back to you.

## When to use it

| Use `wick_run_code` | Use `wick_execute` |
|---|---|
| more than ~3 calls, or a loop over tickets / rooms / rows | one small call |
| a call that needs the previous call's result | one write you want to see on its own |
| a big response you would only filter, count, join or diff | a result you need in full |

## Writing the script

Get the exact tool_ids from `wick_search` / `wick_get`. `wick_get` at op level
also returns `ts`: the ready-made call line with typed params — paste it.

```ts
// Tools
const listIssues = "conn:<id>/list_issues";   // Tracker: team board
const getIssue   = "conn:<id>/get_issue";     // Tracker: team board

const list = await wick.call(listIssues, { state: "open", limit: 200 });
const stale = list.items.filter(i => Date.parse(i.updated_at) < Date.now() - 7 * 864e5);
const details = wick.callMany(stale.map(i => ({ toolId: getIssue, params: { id: i.id } })));
return { open: list.items.length, stale: details.filter(d => d.ok).map(d => d.result.title) };
```

- Every tool_id goes in a named constant at the top with a comment naming the
  connector; every tool_id written in the script is checked before the run.
- `await wick.call(toolId, params)` returns the parsed result or throws.
  `wick.callMany([{toolId, params}])` runs in parallel and returns
  `[{ok, toolId, result|error}]`. `wick.tool(toolId)` gives a callable.
- `input` is the `input` argument (JSON). `console.log/warn/error` go to `logs`.
- Top-level `await` and `return` work. Calls run as this session, with its access.

## script_path

Pass `script_path` instead of `code` for a script you reuse: a file relative
to the session's project folder (no `..`, no absolute path, max 256 KB), read
fresh on every run. Vary it through `input`, not by editing the file.

## wick.file — the codemode/ exchange folder

Scripts may read and write files ONLY inside `codemode/` of the project folder.
Paths are relative to it (`"items.json"` = `<project>/codemode/items.json`).

| call | returns |
|---|---|
| `wick.file.read(path)` / `wick.file.write(path, text)` | text / `{path, bytes}` |
| `wick.file.readJSON(path)` / `wick.file.writeJSON(path, value)` | value / `{path, bytes}` (pretty, 2 spaces) |
| `wick.file.list(dir?)` | `[{path, size, modified}]` |
| `wick.file.exists(path)` | `true` / `false` |

Absolute paths, `..`, symlinks leading out of `codemode/` and folders as files
are refused. Writes create subfolders. Caps: 1 MB per read or write, 20 MB and
1000 files for the whole folder. Every file op is listed in the run's `calls`
(`file:write codemode/result.json`).

Pattern — big input in, big output out, a small summary back:

1. You write `codemode/items.json` (your own file tool) with the ids to process.
2. The script:
   ```ts
   const getRoom = "conn:<id>/get_room"; // Chat: support app
   const ids: string[] = wick.file.readJSON("items.json");
   const rows = wick.callMany(ids.map(id => ({ toolId: getRoom, params: { room_id: id } })));
   wick.file.writeJSON("result.json", rows);
   return { total: ids.length, failed: rows.filter(r => !r.ok).length };
   ```
3. You read `codemode/result.json` only if the summary says you need it.

`wick_run_code` uses the calling session's project folder; a session without a
project gets a clear "no project folder" error.

## Limits

- Result over 20 KB is cut: filter or aggregate in the script.
- At most 1000 calls (file ops included) per run.
- Timeout 30 s by default, `timeout_ms` up to 120 s.
- No `fetch`, timers, `require`, `process` or filesystem besides `wick.file`:
  connectors are the only way out.
- The runner is downloaded automatically the first time a run finds none
  installed (unless the host turned that off). A "runner too old" error means
  the `codemode_goja` plugin needs an update.

## In a watch (kind=code)

A code step reports what is NEW since the last fire. It gets
`input.snapshot` (what it returned last time, `null` first), `input.prev` and
`input.params`, and returns `{changed, snapshot, done?}`; `changed: []` = pending.

```json
{"name": "new rows", "kind": "code", "script_path": "watches/new_rows.ts", "params": {"table": "orders"}}
```

```ts
const query = "conn:<id>/query"; // DB: reporting
const rows = await wick.call(query, { table: input.params.table, since: input.snapshot ?? 0 });
return { changed: rows, snapshot: rows.length ? rows[rows.length - 1].id : input.snapshot };
```

`wick.file` there uses the watch's project folder. Full watch rules: the
`wick-schedules` skill.

## In a workflow (run_code node)

Fields `code` or `script_path`, `workspace` (project id: required with
`script_path` or `wick.file`), `lang`, `input`, `timeout_sec`. Runs as the
workflow creator. See the `wick-workflows` skill.
