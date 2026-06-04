# Environment Variables

Copy `.env.example` to `.env` at the project root:

```bash
cp .env.example .env   # macOS/Linux
copy .env.example .env  # Windows
```

Every variable has a working default — the app boots without any configuration.

---

## Server

### `PORT`
**Default:** `9425`

HTTP listen port. `9425` spells "WICK" on a T9 keypad — picked to avoid collisions with common dev ports (3000 React, 5173 Vite, 5432 Postgres).

```env
PORT=9425
```

When running under the desktop tray, the resolution order is `PORT` env → `port` in `config.json` → built-in default. See [Desktop Tray ▶ Port](/guide/desktop-tray#port).

---

## Database

### `DATABASE_URL`
**Default:** `wick.db` (SQLite file in the project root)

Leave blank to use SQLite — no database setup required. SQLite is fine for local development and small deployments.

```env
# SQLite (default — no config needed)
DATABASE_URL=

# PostgreSQL
DATABASE_URL=postgres://user:password@localhost:5432/myapp?sslmode=disable
```

---

## App

### `APP_NAME`
**Default:** _(empty — falls back to `"Wick"`)_

App name shown in the UI **and** used to namespace per-app paths
(`~/.<app>/`) for config / DB / logs / agents. Only used on first boot
to seed the database display name; the `~/.<app>/` directory layout is
fixed for the life of the install. After first boot the display name
can be changed from `/admin/configs` — the database value always wins.

At build time (`wick build`) the same variable bakes the app name into
the binary via `app.BuildAppName`, used as the default MCP server name
and the per-app data dir.

The `~/.<app>/` tree currently includes:

| Path | What lives there |
|---|---|
| `~/.<app>/wick.db` | SQLite database (when `DATABASE_URL` is blank) |
| `~/.<app>/config.json` | Userconfig — provider instances, status cache, misc kv |
| `~/.<app>/INITIAL_CREDENTIALS.txt` | Auto-generated admin passphrase (deleted on first password rotation) |
| `~/.<app>/logs/{app,server,worker,gate}-YYYY-MM-DD.log` | Daily tail logs |
| `~/.<app>/agents/` | [Agents](../guide/agents) subsystem state — projects, sessions, presets, gate spec/socket |

```env
APP_NAME=My Internal Tools
```

### `APP_URL`
**Default:** `http://localhost:9425`

Base URL used for SSO callbacks and absolute links. Also drives the **host allowlist** — requests whose `Host` header (or `X-Forwarded-Host`) doesn't match this URL's host get a 403. `/health` is exempt.

The env var overrides the DB value at read time (and read-only-locks the row in `/admin/variables`). Useful for bootstrapping on a remote host where the seeded `localhost` value would block your first login.

```env
APP_URL=https://tools.example.com
```

### `ALLOWED_ORIGINS`
**Default:** _(empty — only `APP_URL` is allowed)_

Comma-separated list of extra URLs (or bare `host:port`) added to the host allowlist alongside `APP_URL`. Overrides the `allowed_origins` kvlist in `/admin/variables` at read time.

```env
ALLOWED_ORIGINS=http://192.168.1.42:9425,http://10.0.0.5:9425
```

::: tip LAN / Termux access
On Termux (and any host where `localhost` isn't enough) open `/admin/variables`, click **Detect LAN URLs** to see your reachable IPv4 addresses, and paste them into the `allowed_origins` row. The `install.sh` script also prints your private-range IPs at the end of a Termux install — copy from there if the admin UI isn't reachable yet, and bootstrap with `ALLOWED_ORIGINS=http://<ip>:9425 ./<app> server`.

Suggestions are read-only by design: the install script never writes the allowlist for you because a phone may be on public Wi-Fi where exposing the manager to every device on the SSID would be unsafe.
:::

---

## Admin

### `APP_ADMIN_EMAILS`
**Default:** `admin@admin.com`

Comma-separated list of emails automatically granted the admin role on first login. Env-only by design — admins cannot remove themselves from this list via the UI.

```env
APP_ADMIN_EMAILS=alice@example.com,bob@example.com
```

### `APP_ADMIN_PASSWORD`
**Default:** *(empty — auto-generated 5-word passphrase)*

Seeds the password for the admin account created on first boot. When unset (or left as the historical `"admin"`) wick generates a 5-word passphrase and writes it to `~/.<app>/INITIAL_CREDENTIALS.txt` — operators can recover it from disk, the tray menu (**About → Open default password**), or the stdout banner on headless runs.

Re-seeded on every boot until the admin completes `/profile/setup` (which sets `admin_password_changed=true` and deletes the credentials file). After that, this env is ignored.

```env
APP_ADMIN_PASSWORD=changeme
```

---

## Build-time

These are read by [`wick build`](./build), not by the running binary. They populate `app.BuildAppName` / `BuildAppVersion` / `GitHubPAT` / `GitHubRepo` via Go ldflags. Each falls back to the matching field in `wick.yml` (or empty for the GitHub pair) when not set.

### `APP_NAME`
**Default:** `name:` from `wick.yml` (else `"app"`)

Doubles as runtime display name (see above) and build-time bake. At build time it's stamped into `app.BuildAppName` — used to namespace config / DB / log paths and as the default MCP server name.

```env
APP_NAME=myapp
```

### `APP_VERSION`
**Default:** `version:` from `wick.yml` (else `"dev"`)

Bakes the app version. Shown in the tray title and About menu, advertised by MCP.

```env
APP_VERSION=1.2.0
```

### `RELEASE_GITHUB_PAT`
**Default:** _(empty — self-updater disabled)_

GitHub fine-grained PAT with `Contents: read` on the releases repo. Embedded into the binary so it can poll `releases/latest`. Pair with `RELEASE_GITHUB_REPOSITORY`.

See [`wick build` reference ▶ PAT setup](./build#pat-setup) for scopes and rotation.

### `RELEASE_GITHUB_REPOSITORY`
**Default:** _(empty — self-updater disabled)_

Releases repo in `owner/repo` form. Named `RELEASE_GITHUB_REPOSITORY` (not `GITHUB_REPOSITORY`) because GitHub Actions auto-injects `GITHUB_REPOSITORY` to the source repo and silently blocks step-level overrides — using the prefixed name keeps CI working.

```env
RELEASE_GITHUB_REPOSITORY=acme/myapp-releases
```

---

## UI Stack

Wick uses **Tailwind CSS** for styling and **[templ](https://templ.guide)** for HTML templating. Both are set up automatically by `go run . setup` — no manual configuration needed.

| Tool | What it does | Managed by |
|------|-------------|------------|
| [Tailwind CSS](https://tailwindcss.com) | Utility-first CSS | `wick.yml` setup task downloads the standalone CLI |
| [templ](https://templ.guide) | Type-safe Go HTML templates | `wick.yml` setup task installs `templ` via `go install` |

The `go run . dev` command runs `templ generate` and rebuilds CSS automatically before starting the server.

::: tip For AI agents
Tailwind classes live in `.templ` files only. Never edit `*_templ.go` by hand — it is regenerated by `templ generate`.
:::

---

## Command Gate

The [Command Gate](../guide/command-gate) sidecar (`<app>-gate`) reads no environment variables. Earlier iterations had `WICK_GATE_BIN` / `GATE_BIN` / `WICK_GATE_SPEC` / `GATE_SPEC` — all dropped. Resolution is automatic:

1. Sibling-of-executable: `<app>-gate[.exe]` next to the main binary (shipped by `wick build --installer`).
2. Embedded extract: unpacked from the main binary on first use.
3. `PATH`: last-ditch lookup of `<app>-gate`.

Override the binary location only by placing your replacement in one of those three spots — there's no env var fallback.
