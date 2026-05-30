# Termux / Android

Wick agents can run on Android phones via [Termux](https://termux.dev) — useful for always-on bots on a spare phone, no VPS required. The standard `linux/arm64` build from each release works out of the box, including on devices that still ship a 4.x kernel.

## Background

Before v0.14.12, `wick-agent server` could crash on Termux with `SIGSYS: bad system call` on devices whose Android kernel is older than 5.8:

```
SIGSYS: bad system call
internal/syscall/unix.faccessat2(...)
internal/syscall/unix.Eaccess(...)
os/exec.findExecutable(...)
os/exec.LookPath(...)
github.com/yogasw/wick/internal/agents/provider.Probe(...)
```

The trigger was Go's standard library `os/exec.LookPath`, which uses the `faccessat2(2)` syscall (number 439, added in Linux 5.8). Android's seccomp filter rejects unknown syscalls with `SIGSYS` (process kill) instead of the `ENOSYS` Go's runtime falls back on, so the fallback never fires.

Affected example device: Android 13 userland on top of kernel `4.14.186`, aarch64 — common on phones that shipped before mid-2022 and never received a kernel upgrade.

## The fix

Every code path inside `wick-agent` that called `exec.LookPath` (or `exec.Command(bareName, ...)`, which calls `LookPath` internally) now routes through `internal/safeexec`. That package mirrors the stdlib semantics but uses `os.Stat` + file mode bits to check executability — no `faccessat2`, no syscall Android's seccomp blocks.

Trade-off: `safeexec.LookPath` doesn't honour `AT_EACCESS` (effective-uid permission check). That only matters for setuid binaries, which wick-agent is never deployed as, so real-uid mode bits are the correct authority anyway.

## Install on Termux

The regular `install.sh` handles Termux automatically:

```bash
# In Termux:
pkg install curl
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
wick-agent server
```

::: tip Storage / network access
If you want the agent to read SMS, get GPS, send notifications, etc., install [Termux:API](https://wiki.termux.dev/wiki/Termux:API) (`pkg install termux-api`) and call its CLI tools (`termux-sms-list`, `termux-location`, `termux-notification`) from your agent via `exec.Command`. The Termux:API app must come from F-Droid or GitHub — Play Store builds are too old.
:::

## Verifying the binary is safe

If you build from source, confirm no `exec.LookPath` callsites slipped through:

```bash
go build -tags headless ./internal/agents/...
grep -RIn 'exec\.LookPath' internal/agents/ internal/pkg/api/ app/
# Only matches should be inside internal/safeexec/lookpath.go itself.
```

## Contributing — keep it safe

If you're adding code inside `wick-agent` paths, use `internal/safeexec` instead of `os/exec.LookPath`. See the [contributing guide](/contributing#code-conventions) for the full rule. CLI tooling under `cmd/cli/` and `internal/builder/` runs on the developer's host and can keep using stdlib `exec.LookPath`.
