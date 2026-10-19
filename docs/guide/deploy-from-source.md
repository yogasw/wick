# Build & Deploy from Source

Build a wick app (a project made with `wick init`, e.g. your own `myapp`) and its plugins
from **source** and install or update it, in one command, like the normal installer but from
your repo instead of a GitHub release. On a fresh machine it first installs what the build
needs: Git, Go (the version your `go.mod` asks for) and the wick CLI, so nothing has to be
set up by hand.

For release builds (no Go needed) use the normal installer: [Wick Agent quick install](/guide/agents-only#quick-install).

## Quickstart

::: code-group

```bash [Linux / macOS]
# fresh machine: Go + wick CLI, clone, build, install, plugins
curl -fsSL https://yogasw.github.io/wick/deploy-from-source.sh \
  | bash -s -- all --git https://github.com/org/myapp.git

# every update after that, from inside the checkout
cd ~/myapp && curl -fsSL https://yogasw.github.io/wick/deploy-from-source.sh | bash -s -- all
```

```powershell [Windows]
# fresh machine: Git, Go + wick CLI (per-user, no admin), clone, build, install, plugins
& ([scriptblock]::Create((iwr -useb https://yogasw.github.io/wick/deploy-from-source.ps1))) all -Git https://github.com/org/myapp.git

# every update after that, from inside the checkout
cd ~\myapp; & ([scriptblock]::Create((iwr -useb https://yogasw.github.io/wick/deploy-from-source.ps1))) all
```

:::

Private repo: set a token first. It is sent as a header, never written to `.git/config`:

::: code-group

```bash [Linux / macOS]
curl -fsSL https://yogasw.github.io/wick/deploy-from-source.sh \
  | TOKEN=ghp_xxx bash -s -- all --git https://github.com/org/myapp.git
```

```powershell [Windows]
$env:TOKEN = 'ghp_xxx'
& ([scriptblock]::Create((iwr -useb https://yogasw.github.io/wick/deploy-from-source.ps1))) all -Git https://github.com/org/myapp.git
```

:::

::: tip Script URL before a release
`yogasw.github.io/wick/…` is published with the docs on every wick release. Until a release
includes these scripts, use the raw files from a branch:
`https://raw.githubusercontent.com/yogasw/wick/master/scripts/deploy-from-source.sh`
(`.ps1` for Windows; `/develop/` for the newest version).
:::

Add `--dry-run` (`-DryRun` on Windows) to print every step without changing anything.

| | Linux | macOS | Windows |
|---|---|---|---|
| Installs Git / Go / wick CLI | apt, dnf or apk · Go in `/usr/local/go` | Homebrew for Git · Go in `/usr/local/go` | winget for Git · Go in `%LOCALAPPDATA%\Programs\Go` (no admin) |
| First install | `.deb` (`dpkg -i`), else `/usr/local/bin/<app>` | `/usr/local/bin/<app>` | `%LOCALAPPDATA%\Programs\<app>\<app>.exe`, added to your PATH |
| Update | zero-downtime `reload` | zero-downtime `reload` | stop, swap the `.exe`, start (Windows has no socket handover) |

## Commands

| Command | What it does |
|---|---|
| `setup` | Installs git/curl/tar (apt, dnf or apk), Go into `/usr/local/go` when missing or older than `go.mod` needs, and the wick CLI at the version your app depends on. |
| `core` | `git pull`, bumps the patch `version:` in `wick.yml`, `wick upgrade -y` (latest wick release), `go test`, `wick build`, then installs the build (below). |
| `plugins` | Builds and installs every plugin under `plugins/{connector,tool,job,service}/<key>/` whose `VERSION` is newer than the installed one, or not installed yet. Name keys to force: `plugins my_tool other_tool`. |
| `all` | `setup`, `core`, `plugins`. |
| `status` | App version and `plugin list`. |

How `core` installs the build (Linux / macOS):

- **App already installed and running**: `<app> reload --binary bin/<app>-linux-<arch> --sudo -y --wait`.
  The new binary is checked (same wick app, arch, newer version), swapped in atomically, takes
  over the listening socket while the old process finishes its in-flight work, and is rolled
  back if it never starts serving. The daemon needs `WICK_GRACEFUL_UPGRADE=1` (set by the
  packaged systemd unit); without it the reload is refused and nothing changes. Rerun with
  `--restart` to stop, swap and start instead (a short downtime).
- **Not installed yet**: installs the `.deb` from `bin/` (Linux with dpkg) or copies the binary
  to `/usr/local/bin/<app>`. Start it with `<app> start`.

Plugins are hot-loaded by the running app; the script waits until `plugin list` shows the new
version. A plugin is never downgraded, so one installed from another source with a higher
version is left alone.

## Flags

| Flag | Default | |
|---|---|---|
| `--dir DIR` | current dir if it has `wick.yml`, else `~/<repo name>` | app checkout |
| `--git URL` | | clone into `--dir` when it is not there yet (https recommended) |
| `--branch NAME` | repo default | branch to clone / pull |
| `--app NAME` | `name:` in `wick.yml` | installed binary name |
| `--wick DIR` | | build against a local wick checkout (temporary `go.mod` replace, always restored) |
| `--no-upgrade` | | keep the wick version in `go.mod` |
| `--no-pull` / `--no-bump` / `--no-test` | | skip that step |
| `--all-plugins` | | rebuild every plugin |
| `--bump-plugins` | | bump each built plugin's `VERSION` patch first |
| `--restart` | | stop / swap / start instead of `reload` (short downtime) |
| `--dry-run` | | print, change nothing |

On Windows the same flags are PowerShell switches: `-Dir`, `-Git`, `-Branch`, `-App`, `-Wick`,
`-NoUpgrade`, `-NoPull`, `-NoBump`, `-NoTest`, `-AllPlugins`, `-BumpPlugins`, `-DryRun`.

## Build by hand

What the script runs, if you prefer the steps yourself:

```bash
# 1. toolchain
curl -fsSL -o go.tgz https://go.dev/dl/go1.26.0.linux-amd64.tar.gz   # the version in go.mod
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go.tgz
export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH
go install github.com/yogasw/wick@latest                          # the wick CLI

# 2. app
git clone https://github.com/org/myapp.git && cd myapp
wick upgrade -y                     # latest wick release into go.mod
wick build --target linux/amd64     # bin/myapp-linux-amd64 + bin/myapp-<ver>-linux-amd64.deb

# 3. install: first time / every update after that
sudo dpkg -i bin/myapp-*-linux-amd64.deb && myapp start
myapp reload --binary bin/myapp-linux-amd64 --sudo -y --wait

# 4. a plugin
cd plugins && GOWORK=off wick plugin build --kind tool my_tool --target linux/amd64
myapp plugin install bin/my_tool-0.1.0-linux-amd64.zip
```

Bump `version:` in `wick.yml` before every update: `reload` refuses a build that is not newer
than the running one. `GOWORK=off` matters when the repo has a `go.work`; without it the plugin
build fails with "-mod may only be set to readonly or vendor".

## Shared build hosts

If the machine has a `buildq` command on `PATH` (a host-wide build queue), tests and builds
run through it, one heavy job at a time; otherwise they run at low priority (`nice`).
