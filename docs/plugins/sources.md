# Plugin sources

**Admin → Plugins → Add new plugin** has three ways in. All of them end in the
same verification and the same kind folders.

| Method | Input | New versions detected | Notes |
|---|---|---|---|
| **Upload** | a `.zip` (binary + `plugin.json`), max 100 MB | no — upload again | dev / local testing |
| **Link** | URL to a `plugins.json`, or straight to a `.zip` | `plugins.json`: yes (polling) · zip: no | a zip link is a one-off install |
| **GitHub release** | `owner/repo`, public or private (+ PAT), optional key filter | yes, via the Releases API | assets must be on a release, not files in a branch |

From the shell: `<app> plugin source add <https-url-to-plugins.json|owner/repo>`,
`plugin source list`, `plugin source check <id>`, `plugin source remove <id>`.

## GitHub release rules

- Tag each release `<key>/v<version>`. One repo can hold many plugins; each
  has its own releases. Tags without a `/` (core `v1.2.3`) are ignored.
- Release assets: `plugins.json` (one entry, relative `url`) + one zip per
  os/arch. `wick plugin index` and the reusable
  [release workflow](./release.md) produce exactly that.
- Detection: list releases (newest 50), group by key, take the highest
  version per key — not GitHub's "latest" — and read that release's
  `plugins.json`. Pre-releases count only when the source allows them.
- **Private repos:** assets are downloaded through the asset API with
  `Accept: application/octet-stream` and the PAT; `browser_download_url`
  does not work for private repos.

## PAT

Use a **fine-grained** token limited to that one repository with
`Contents: Read-only`. It is stored encrypted and never shown again. It is
only sent to the GitHub API host, never to URLs found inside an index. Public
repos work without one (60 requests/hour per IP; polling + ETag stays far
below that).

## Polling and updates

- Every source is re-checked every 30 minutes by default (per source
  setting), with ETag. *Check now* runs it immediately.
- A newer version shows *Update available* on the plugin card and
  *Update to vX* in the kebab of the plugin's detail page (admin only).
- `auto_update` is off by default and can be enabled per source.

## Test (health check)

The **Test** button on a source runs six steps and reports each one:

1. **Reachable** — HTTPS (plain http only for localhost).
2. **Auth** — the PAT can read the repo.
3. **Index** — `plugins.json` found and valid (keys, semver).
4. **Asset for host** — an entry for this host's os/arch with a correctly
   named zip.
5. **Dry-run** — download, `zip_sha256`, signature, extract, manifest checks;
   nothing is installed.
6. **After install** — the installed plugin's `Health`; *skip* until
   something from the source is installed.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `404` on a private repo | the PAT has no access to that repo (fine-grained scope) — GitHub answers 404, not 403 |
| `403` | rate limit without a PAT, or the token is expired |
| "has no build for linux/amd64" | the release has no zip for this host's os/arch |
| "file name is not `<key>-<version>-<os>-<arch>.zip`" | renamed asset, or a key with `-` |
| "zip_sha256 mismatch" | the zip was replaced after `plugins.json` was written — regenerate the index |
| "signature required but the index entry is unsigned" / "signature verification failed" | the source pins a key (or `WICK_PLUGIN_REQUIRE_SIGNATURE=1`) and the release was not signed with the matching private key |
| "index says kind X but plugin.json says Y" | index and zip disagree — rebuild both |
