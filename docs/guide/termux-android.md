# Termux / Android

Wick agents can run on Android phones via [Termux](https://termux.dev) — useful for always-on bots on a spare phone, no VPS required.

## The problem with `linux/arm64` builds on Termux

The `linux/arm64` binary published in releases crashes on Termux with `SIGSYS: bad system call`:

```
SIGSYS: bad system call
PC=0xaaaad1cabaa4 m=8 sigcode=1
signal arrived during cgo execution

goroutine 153 gp=0x4000582380 m=8 mp=0x4000601008 [syscall]:
runtime.cgocall(0xaaaad1cabab0, 0x4001327748)
...
internal/syscall/unix.faccessat2(...)
internal/syscall/unix.Eaccess(...)
os/exec.findExecutable(...)
os/exec.LookPath(...)
github.com/yogasw/wick/internal/agents/provider.Probe(...)
github.com/yogasw/wick/internal/agents/provider.scanKnownLocations(...)
```

**Why:** Go 1.20+ uses the `faccessat2` Linux syscall (number 439, added in kernel 5.8) for `exec.LookPath` permission checks. Many Android 11/12/13 devices still ship a 4.x kernel (Android version is userland; kernel is independent). On those kernels Android's seccomp filter rejects unknown syscalls with `SIGSYS` — killing the process before Go's `ENOSYS` fallback can trigger.

Affected example device: Android 13, kernel `4.14.186`, aarch64 — seen on the user-reported crash that prompted this doc.

## The fix: build with `GOOS=android`

Go's standard library short-circuits `Eaccess()` to return `ENOSYS` immediately when `GOOS=android`, which dead-code-eliminates the `faccessat2` syscall path entirely. `os/exec.LookPath` then falls back to checking the executable bits directly — no risky syscall, no SIGSYS.

```bash
wick build --target android/arm64
```

`--headless` is auto-enabled for android targets (systray is CGO/X11-only, unavailable on Android). The output binary lives at `bin/<app>-android-arm64`.

## Install on Termux

```bash
# On your build host (Linux/macOS x86_64 or arm64):
cd ~/wick-agent
wick build --target android/arm64

# Transfer to phone:
adb push bin/wick-agent-android-arm64 /sdcard/Download/

# In Termux (on the phone):
cp /sdcard/Download/wick-agent-android-arm64 $PREFIX/bin/wick-agent
chmod +x $PREFIX/bin/wick-agent
wick-agent server
```

::: tip Storage access
Run `termux-setup-storage` once on the phone before the `cp` step so Termux can read `/sdcard/Download/`.
:::

## What you lose on android target

- **Systray** — no tray icon, no GUI-driven start/stop. Use `wick-agent server` directly or wrap in [`termux-services`](https://wiki.termux.dev/wiki/Termux-services) for autostart.
- **`.deb` package** — the build produces only the raw binary (Android has no `dpkg`). Install by `cp` into `$PREFIX/bin/` as shown above.
- **CGO-dependent connectors** — anything requiring CGO will not be available since android builds default to `CGO_ENABLED=0`.

## Verifying the binary is faccessat2-free

```bash
go tool nm bin/wick-agent-android-arm64 | grep faccessat
# (no output expected — symbol is dead-code-eliminated)

# Compare against a linux/arm64 build:
go tool nm bin/wick-agent-linux-arm64 | grep faccessat
# syscall.Faccessat
# syscall.faccessat
# syscall.faccessat2   <-- this one triggers SIGSYS on old kernels
```

## Why not patch the Go toolchain instead?

A toolchain patch (e.g. forcing `faccessat2 = -1` to always fall back) was considered but rejected:

- **Doesn't help on Android's seccomp.** The filter blocks *unknown* syscall numbers — `-1` triggers SIGSYS just like `439`. Only skipping the syscall entirely (which `GOOS=android` already does) works.
- **Maintenance burden.** Every Go release would need re-patching.
- **Already solved upstream.** Go's `Eaccess()` for `GOOS=android` is the upstream solution — wick just needs to use it.
