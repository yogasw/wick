# Slow boot: two stalls before the HTTP port opens

Boot visibly hangs twice — once after `db: sqlite`, again around
`connector plugins: loaded` — and only then does the server start listening.
This note records what was found so the investigation can resume without
re-deriving it.

## TODO

- [ ] Measure the two gaps for real. Add temporary `time.Since(start)` logs
      around `configsSvc.Bootstrap`, `connplugin.Load`, and the listen call in
      `internal/pkg/api/server.go`, then paste one cold-boot log here. Nothing
      below is confirmed timing — only the code path is confirmed.
- [ ] Confirm suspect #1 (plugin verification) by timing a SHA-256 of the 11
      plugin binaries on this machine, cold cache. Command was drafted but not
      run: `for f in ~/.wick-lab/plugins/connectors/*/*.exe; do sha256sum "$f"; done`
- [ ] Decide the fix for #1 (options listed below).
- [ ] Check whether `Scan(dir)` running twice is measurable or just untidy.
- [ ] Rule in/out suspect #2 (`configsSvc.Bootstrap`) — this one is pure
      guesswork so far, no evidence gathered.

## Observed

```
{"message":"db: sqlite","path":"C:\\Users\\Staffinc\\.wick-lab\\wick.db"}
   ← stall
{"message":"connector plugins: loaded"}
   ← stall
   ← server spawns, HTTP reachable
```

Reported as intermittent ("kadang stuck di sini lama"), which fits a
filesystem/AV-cache effect more than a fixed cost.

## Suspect #1 — plugin verification hashes 139 MB, synchronously, before listen

`internal/pkg/api/server.go:258` calls `connplugin.Load(...)` on the boot
path, before the server listens. That walks every installed plugin and calls
`wickplugin.VerifyManifest` (`pkg/plugin/manifest.go:116`), which does
`sha256File(binaryPath)` per plugin.

Installed on this machine — 11 plugins, **139 MB total**:

| plugin | size |
|---|---|
| bitbucket | 14.2 MB |
| git | 14.6 MB |
| github | 14.3 MB |
| google_workspace | 14.4 MB |
| httpbin | 14.1 MB |
| loki | 14.1 MB |
| notion | 14.2 MB |
| notion_unofficial | 14.3 MB |
| phoenix | 14.1 MB |
| playwright_browser | 16.5 MB |

On Windows every one of those reads goes through Defender, which would explain
both the delay and why it varies run to run (warm vs cold file cache).

`Scan(dir)` also runs **twice** per load — once in `Load`, once inside
`loadWith` — so every manifest is read and parsed twice. Cheap next to the
hashing, but it is doing the walk twice for no reason.

### Options for #1

Not yet decided; each trades away something different.

1. **Verify in the background, gate dispensing on it.** Register plugins
    immediately, let `WarmUp` (already a goroutine) verify, and refuse to hand
    out a lease for a plugin whose hash has not checked out yet. Boot stops
    waiting; the integrity guarantee moves from "before listen" to "before
    first use". Needs care: the check must be impossible to skip, or it stops
    being a check.
2. **Cache the hash, keyed by (path, size, mtime).** Skip re-hashing a binary
    that has not changed since the last boot. Fast and keeps verification on
    the boot path, but a cache is now a thing that can be stale or poisoned —
    the key has to be something an attacker replacing the binary cannot keep
    constant, and mtime is not that.
3. **Hash the plugins in parallel.** Simplest, keeps every current guarantee,
    and if the cost is Defender-per-read rather than CPU it may buy little.

Worth knowing before choosing: how much of the time is CPU and how much is
the AV/filesystem. Option 3 helps the first, not the second.

## Suspect #2 — the gap before `plugin loaded`

Between `db: sqlite` and the plugin line, boot does module discovery +
validation and then `configsSvc.Bootstrap` (`server.go:249`), which reconciles
every module's and job's config rows against the `configs` table.

Guess only — no measurement yet. Worth an eye on whether Bootstrap does one
statement per row rather than batching, since the row count grows with every
module and job registered.

## Not yet looked at

The third stall — `plugin loaded` → server actually listening. Whatever runs
between them (SSO provider load, channel setup, job registration) has not been
read yet.
