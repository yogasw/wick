# Termux / Android

Run a Wick agent on an Android phone via [Termux](https://termux.dev) — handy for always-on bots on a spare phone without spinning up a VPS. The standard `linux/arm64` release binary works out of the box.

## Install

In Termux:

```bash
pkg install curl
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
wick-agent server
```

Open `http://localhost:9425` in the phone's browser to reach the admin UI, then add your AI provider keys and start the agent.

::: tip Reaching the agent from another device
By default the server binds to localhost. To reach it from your laptop, either open the URL on the phone itself or set up Termux's [SSH access](https://wiki.termux.dev/wiki/Remote_Access) and forward port `9425`.
:::

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
wick-agent server
```

Add `termux-wake-lock` to your `.bashrc` if you want it permanent. For unattended autostart on boot, see [`termux-services`](https://wiki.termux.dev/wiki/Termux-services).

## What you don't get on Termux

- **System tray / GUI** — no desktop to attach to. Run `wick-agent server` directly.
- **Installer packaging** — `.deb` / `.dmg` / `.msi` formats don't apply; the raw binary `install.sh` fetches is what runs.
