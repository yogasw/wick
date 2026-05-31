# Termux / Android

Run a Wick agent on an Android phone via [Termux](https://termux.dev) — handy for always-on bots on a spare phone without spinning up a VPS. The standard `linux/arm64` release binary works out of the box.

## Install

In Termux:

```bash
pkg install curl
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
wick-agent start
```

`wick-agent start` spawns the binary detached (server + worker via `wick-agent all`), writes a PID file to `~/.wick-agent/run.pid`, and pipes output to `~/.wick-agent/daemon.log`. Closing the Termux session does not kill it.

Open `http://localhost:9425` in the phone's browser to reach the admin UI, then add your AI provider keys and start the agent.

::: tip Reaching the agent from another device
By default the server binds to localhost. To reach it from your laptop, either open the URL on the phone itself or set up Termux's [SSH access](https://wiki.termux.dev/wiki/Remote_Access) and forward port `9425`.
:::

## Start / stop / status

The agent runs as a daemon. Manage it with:

```bash
wick-agent start          # spawn detached (or no-op if already running)
wick-agent status         # PID, uptime, log/pidfile paths
wick-agent status --log 1000   # tail last 1000 bytes of daemon log
wick-agent stop           # SIGTERM, escalates to SIGKILL after timeout
wick-agent restart        # stop + start
```

Full subcommand list: [App CLI Reference](/reference/app-cli).

## Install AI provider CLIs

Wick spawns the provider CLI (`claude`, `codex`, `gemini`) as a subprocess — they're **not** bundled. You need to install at least one before the agent can answer anything.

### Claude Code

::: warning Upstream installer broken on Termux
Anthropic's official Claude Code installer assumes glibc + a writable `/etc`, neither of which Termux provides. Use the community workaround (we host a copy):
:::

```bash
curl -fsSL https://yogasw.github.io/wick/install-claude-termux.sh | bash
```

What it does:
1. Installs glibc-runner + patchelf from the `glibc-repo` Termux package
2. Downloads the official `linux-arm64` Claude binary, verifies SHA256 against upstream manifest
3. `patchelf --set-interpreter` so the kernel can exec it directly under glibc's `ld.so` (needed for argv[0]-preserving re-exec)
4. Writes a wrapper at `~/.local/bin/claude` that unsets `LD_PRELOAD` before exec
5. Disables `autoUpdates` in `~/.claude/settings.json` (re-run the script to upgrade)

After install:

```bash
source ~/.bashrc       # picks up ~/.local/bin in PATH
claude --version
claude login           # OAuth or API key
```

Then in wick admin UI → Providers → add Claude, point the binary path at `~/.local/bin/claude`.

Credit: workaround originally from [anthropics/claude-code#50270](https://github.com/anthropics/claude-code/issues/50270#issuecomment-4584920515).

### Codex CLI

Codex's official installer works on Termux:

```bash
curl -fsSL https://chatgpt.com/codex/install.sh | sh
```

Then **re-run wick's installer** (idempotent — it only writes the codex alias if missing):

```bash
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
source ~/.bashrc
```

The wick installer auto-appends a `codex` alias to `~/.bashrc` that wraps the binary with `proot --bind` mounts for `/etc/resolv.conf` and `/etc/ssl/certs/ca-certificates.crt`. Without this wrap, `codex login --device-auth` fails with `error sending request for url …auth.openai.com…` because the musl-linked binary hard-codes those paths but Android's `/etc` is read-only.

Workaround source: [netanel-haber/77b91c4148249394d75546348bae7698](https://gist.github.com/netanel-haber/77b91c4148249394d75546348bae7698).

### `codex login` — port-forward from your laptop

`codex login` opens a device-auth callback on `localhost:1455`. On the phone you typically don't have a browser pointed at Termux's localhost — easiest fix is to SSH from your laptop with a port-forward, then run `codex login` over SSH.

**1. Enable Termux SSH server** (one-time):

```bash
pkg install openssh
passwd                  # set a password
sshd                    # start the daemon
whoami                  # → e.g. u0_a411 — your Android username
ip -4 addr | grep inet  # → e.g. 192.168.1.42 — phone LAN IP
```

Termux SSH listens on port **8022** (not 22 — Android won't let unprivileged binaries bind low ports).

**2. SSH from laptop with port-forward:**

```bash
ssh -L 1455:localhost:1455 -p 8022 u0_a411@192.168.1.42
```

Replace `u0_a411` with the `whoami` output and `192.168.1.42` with the phone IP. The `-L 1455:localhost:1455` tunnels the codex auth callback back to your laptop.

**3. Inside the SSH session, run:**

```bash
codex login --device-auth
```

Codex prints a URL — open it **in your laptop browser**. The OAuth callback hits `localhost:1455` on the laptop, which the SSH tunnel forwards to the phone where codex is listening. Login succeeds, token gets written to Termux's `~/.codex/`.

After that, point wick's Provider config at the codex binary path (`$PREFIX/bin/codex`) and the alias handles the proot wrap on every spawn.

## Talking to phone hardware (SMS, GPS, notifications)

Install [Termux:API](https://wiki.termux.dev/wiki/Termux:API) once:

```bash
pkg install termux-api
```

Then install the **Termux:API companion app** from F-Droid or the [GitHub releases](https://github.com/termux/termux-api/releases) page — the Play Store version is too old. Grant SMS / location / notification permissions when prompted.

After that your agent (workflow shell nodes, Go tools, etc.) can call:

| Command | What it does |
|---|---|
| `termux-sms-list` | Dump inbox SMS as JSON |
| `termux-location` | Current GPS coordinates |
| `termux-notification --content "..."` | Push a notification to the status bar |
| `termux-battery-status` | Battery level + temperature |
| `termux-camera-photo -c 0 out.jpg` | Snap a photo |

Each is just a CLI binary — wire it up wherever you'd normally shell out.

## Keeping it running

Android will suspend the process when the screen turns off unless Termux holds a wake lock:

```bash
termux-wake-lock
wick-agent start
```

Add `termux-wake-lock` to your `.bashrc` if you want it permanent.

For unattended autostart on boot, register the daemon with the OS:

```bash
wick-agent service install
```

On Termux this writes a [Termux:Boot](https://wiki.termux.dev/wiki/Termux:Boot) script at `~/.termux/boot/wick-agent-start` that runs `wick-agent start` after device boot. Install the Termux:Boot companion app from F-Droid for it to fire. Details: [App CLI — Auto-start service](/reference/app-cli#auto-start-service).

## Remote access without exposing the port

Unrooted Android has no firewall, so binding `:9425` to all interfaces leaks the admin UI to every device on the Wi-Fi. Two clean options:

- **SSH tunnel** (built-in, no extra dep) — bind to localhost and forward from your laptop. Covered in [Codex login](#codex-login-port-forward-from-your-laptop) above; same pattern works for `9425`.
- **Outbound tunnel** (no port-forward on either side) — set `startup_script` at `/admin/variables` to `ngrok http 9425` or `cloudflared tunnel run my-tunnel`, toggle `startup_script_enabled` on, and restart. The tunnel spawns alongside the server and dies when you stop wick. Details: [Admin Panel — Startup script](./admin-panel#startup-script).

## What you don't get on Termux

- **System tray / GUI** — no desktop to attach to. Use `wick-agent start` (daemon).
- **Installer packaging** — `.deb` / `.dmg` / `.msi` formats don't apply; the raw binary `install.sh` fetches is what runs.
