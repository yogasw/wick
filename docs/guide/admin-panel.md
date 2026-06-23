# Admin Panel

Wick ships a full-featured admin panel at `/admin/*` — no separate codebase, no extra service. Admins manage users, modules, tags, configs, and the MCP auth surface from one place. Non-admins never see these pages.

This page collects screenshots of every admin surface in one place. Each subsection has a one-paragraph summary plus a link to the operational guide.

::: tip Who counts as an admin?
A user becomes admin in one of two ways: their email is in `APP_ADMIN_EMAILS` at first login, or an existing admin promotes them at `/admin/users`. See [Environment Variables](../reference/env-vars#admin) for the env-only bootstrap path.
:::

::: tip App Owner
Above admin sits a single **App Owner**: the first user ever registered is auto-promoted (`is_owner`). The owner is a superset of admin — `IsAdmin()` is true for both — but **only the owner can see every user's agent sessions**. Admins and regular users see only the sessions, projects, workflows, and skills they own or can reach via project tag grants; per-session routes return `404` for sessions outside their access. There is no env var for this — it's assigned automatically to the first account.
:::

::: info Admin session visibility (`admin_see_all`)
By default, admins are scoped exactly like regular users: they see only projects granted via tags and their own sessions. Ownerless sessions (no recorded creator) are hidden from everyone under this default.

To restore the old behaviour where admins see every project and session, turn on **`admin_see_all`** at `/admin/variables`. When on, admins regain an unrestricted view identical to the App Owner's legacy view. The App Owner is always unrestricted regardless of this setting.
:::

## Dashboard

![Admin Dashboard](/screenshots/admin-dashboard.png)
*Dashboard — top-line stats split into Modules (execution health) and Access (auth surface). Clickable Connector / Access Token / Connected App cards jump to the matching admin page.*

The dashboard groups stats into two clusters so module health and auth surface don't visually mix:

- **Modules** — Tools, Jobs, Enabled count, Running count, total Configs, Missing-required-configs count.
- **Access** — Connectors, Access Tokens, Connected Apps. Each card links to the page below.

## Users

![Admin Users](/screenshots/admin-users.png)
*Users — approve accounts, assign roles (admin / user) and access tags. System tags are filtered out of the picker; role auto-syncs system tags on promote/demote.*

Approve newly-registered users, demote/promote roles, and attach access tags. The tag picker hides System tags (code-managed, see [Connector Runs Purge](./connector-runs-purge#system-tag-—-what-makes-this-job-special)).

## Mini Tools (Tools, Connectors, Jobs)

The Tools, Connectors, and Jobs admin pages are grouped under a **Mini Tools** dropdown in the admin navigation bar. A **Mini Tools** link is also available to all users in the Agents sidebar at the bottom, and opens the tools grid at `/mini-tools`.

### Tools

![Admin Tools](/screenshots/admin-tools.png)
*Tool Permissions — enable/disable tools, set per-tool visibility (public/private), assign tags.*

Disabling a tool here is a kill-switch — the tool disappears from the home grid and Ctrl+K palette without a redeploy. Tag assignments here drive the home-grid grouping and (for filter tags) access control.

### Jobs

![Admin Jobs](/screenshots/admin-jobs.png)
*Job Permissions — enable/disable jobs, assign access tags. System-tagged jobs (e.g. `connector-runs-purge`) are locked: no Hide/Show button, no tag mutation, no schedule disable from this page.*

For System-tagged jobs the action buttons are removed and the tag picker is read-only. Manage retention windows and cron schedules from `/manager/jobs/{key}` instead.

### Connectors

![Admin connectors cross-key list](/screenshots/admin-connectors.png)

*`/admin/connectors` cross-Key list — Disabled toggles, per-row tag picker, "Module not registered" badge for orphans.*

`/admin/connectors` is the cross-Key list of every connector row across every user. Toggle `Disabled` to hide a row from MCP `tools/list` and from `/manager/connectors`. The tag picker reuses `ToolTag` with path `/connectors/{id}` — same mechanism as Tools.

A row whose `Key` no longer has a registered module is tolerated: the row stays, marked "Module not registered". `wick_execute` against such a row returns an error.

Operational guide: [Connector Module](./connector-module).

## Projects, Workflows & Skills (ownership)

`/admin/projects`, `/admin/workflows`, and `/admin/skills` are cross-user lists of every project, workflow, and skill, each with a tag picker — the admin surface for the **ownership** model that backs per-user isolation.

- Each of these resources gets an `owner:{resourceID}` filter tag at creation time, and stamps `created_by` as an audit trail. The owner (and any admin) sees it; everyone else is filtered out — same `IsFilter` mechanism as the [Tags](#tags) section, just auto-created per resource instead of hand-assigned.
- **Share** a resource by assigning a group tag to it here (and the same tag to the users/groups who should see it at `/admin/users`). **Transfer or open up** by editing its tags.
- Skills now live in their own DB table with `created_by` set on upload; `wick_skill_sync` over MCP is admin-only.

This is the same tag-filter model used for connectors and tools — the `owner:` tags simply make every user-created resource private-by-default to its creator until explicitly shared.

## Access Tokens

![Admin access tokens cross-user view](/screenshots/admin-tokens.png)

*`/admin/access-tokens` cross-user view — stat card row + table.*

Cross-user view of every active Personal Access Token. Admins can revoke any token without the owner's consent — useful when a token has been compromised or a user has left the team.

Operational guide: [Access Tokens (PAT)](./access-tokens).

## Connected Apps (OAuth)

![Admin connections cross-user grant table](/screenshots/admin-connections.png)

*`/admin/connections` cross-user grant table — one row per (user × OAuth client) with admin Disconnect buttons.*

Cross-user view of every active OAuth grant. Each row is one (user × client) pair with at least one valid access or refresh token. Disconnect revokes every token issued to that client for that user; the client must re-do the OAuth dance to regain access.

Operational guide: [OAuth Connections](./oauth-connections).

## Tags

![Admin Tags](/screenshots/admin-tags.png)
*Tags — create group tags (home grouping) and filter tags (access control). System tags render as "Read-only · code-managed" with no Edit/Delete controls.*

Three orthogonal flags per tag:

- `IsGroup` — buckets modules visually on the home grid.
- `IsFilter` — gates access; rows with ≥1 filter tag are visible only to users carrying a matching tag.
- `IsSystem` — code-owned, immutable from the UI. Used by built-in jobs and connectors that ship with wick.

## Configs

![Admin Configs](/screenshots/admin-configs.png)
*Configs — runtime variables (app name, app URL, SSO providers, OAuth secrets) editable without redeploying. The DB value always wins over env-var seeds.*

Env vars seed the row on first boot only; subsequent edits via this page are durable. SSO providers (Google, etc.) are configured here — no `client_id`/`client_secret` baked into the binary.

Operational guide: [Environment Variables](../reference/env-vars).

## Startup script

`/admin/variables` exposes a `startup_script` textarea and `startup_script_enabled` toggle. When enabled, wick runs the script in a fresh shell every time the server boots — `sh` on Linux/macOS, PowerShell on Windows. Output (stdout + stderr) lands in `~/.<appName>/logs/startup-script-YYYY-MM-DD.log`.

The script + every process it spawns is bound to the server context via a process group (Unix `setpgid` + `kill -pgid`) / Job Object (Windows `KILL_ON_JOB_CLOSE`). Tray Stop, tray Quit, and `wick server` SIGINT all tear down the whole tree — backgrounded `&` daemons cannot survive as orphans. Restart the server to pick up edits; the running process is not HUP'd in-place.

### Multi-line behaviour

The script runs sequentially in one shell process, exactly like a `.sh` / `.ps1` file. A long-running foreground command blocks everything below it:

```bash
ngrok http 9425         # blocks here — line 2 never runs
echo "tunnel up"
```

For multiple daemons in parallel, background each one explicitly:

```bash
# Unix
ngrok http 9425 &
cloudflared tunnel run my-tunnel &
```

```powershell
# Windows
Start-Process ngrok -ArgumentList "http 9425"
Start-Process cloudflared -ArgumentList "tunnel run my-tunnel"
```

All children — direct or backgrounded — die together when the server stops.

### Use cases

- **Tunnel without exposing the LAN port.** Pair `--localhost` (or `WICK_HOST=127.0.0.1`) with a tunnel command:

  ```bash
  ngrok http 9425
  # or
  cloudflared tunnel run my-tunnel
  ```

  Port `:9425` stays bound to loopback, the tunnel terminates TLS on a vendor domain, and the LAN never sees the server. Required pattern on Termux phones where Android has no host firewall.

- **Pre-warm caches or kick off side processes** that should always run alongside the server.

::: warning
Anything in this textarea runs as the wick user with full shell access. It's admin-gated like every other `/admin/*` page, but treat it the same as `session_secret` — anyone who can edit this row can execute arbitrary code on the host.
:::

## Reference

- Module operations: [Tool Module](./tool-module), [Background Job](./job-module), [Connector Module](./connector-module)
- LLM auth: [Access Tokens](./access-tokens), [OAuth Connections](./oauth-connections), [MCP for LLMs](./mcp)
- Maintenance: [Connector Runs Purge](./connector-runs-purge)
