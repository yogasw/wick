# Releasing a plugin

How a connector goes from source to "Available to install" in the wick
marketplace. Most of this is automated by CI — the manual steps are just
authoring the connector and bumping its `VERSION`.

## TWO separate release pipelines, SAME gate (PR → `release`)

Both core and plugin releases are deliberate: they run on a **pull request into
the `release` branch**, not on every push. They're split by path so one PR
triggers only one pipeline.

| PR into `release` touches… | Workflow that runs | What it builds |
|---|---|---|
| only `wick-plugins/**` | `release-plugins.yml` (`paths: wick-plugins/**`) | only the changed plugin(s) → zips → release → catalog |
| only core (anything else) | `release.yml` (`paths-ignore: wick-plugins/**`) | the wick binary |
| both | both run | both, independently |

Why no cross-firing:

- **Plugin pipeline**: PR → `release`, `paths: wick-plugins/connector|tool|job/**`.
  A core-only PR doesn't match → plugins aren't built.
- **Core pipeline**: PR → `release`, `paths-ignore: wick-plugins/**`. A
  plugin-only PR is fully ignored → the core binary isn't rebuilt.
- Plugin release tags are `<name>/v<ver>` (e.g. `httpbin/v0.1.0`) — they start
  with the plugin name, so they never match the core `v*` tag triggers either.

> ⚠️ **Branch protection caveat:** never mark a path-filtered workflow as a
> *required status check*. When the path doesn't match, GitHub never starts the
> check, and a required-but-never-run check leaves the PR stuck "pending". Put
> required checks on the always-run test gate instead.

## The flow (per plugin)

```
1. Author     cp -r connector/_template connector/<name>   (edit connector.go;
              Meta.Key MUST equal the folder name)
2. Version    echo 0.2.0 > connector/<name>/VERSION
3. PR         open a PR from master → release that touches
              wick-plugins/connector/<name>/**
        │
        ▼  release-plugins.yml runs (only this plugin; skipped if its
           <name>/v0.2.0 tag already exists — bump VERSION for a new release)
4. detect       diffs the PR base..head → [{kind:connector, name:<name>}]
5. build        wick plugin build --kind connector <name> --all
                  → <name>-0.2.0-<os>-<arch>.zip per target
6. release      gh release "<name>/v0.2.0"  with all the zips
7. update-catalog
                regenerates plugins.json from ALL live releases
                (latest version per plugin, os/arch → download URL)
                + backfills name + description from each manifest
                + commits plugins.json   [skip ci]
        │
        ▼
8. marketplace  app fetches raw plugins.json → <name> shows under
                "Available to install" → user clicks Download
```

Steps 4–8 are **fully automated**. You only do 1–3 (author, bump VERSION, open the PR).

## Manual release (no CI)

```bash
# from the wick repo root (go.work resolves the local pkg/plugin)
go build -o /tmp/wick .

cd wick-plugins
/tmp/wick plugin build --kind connector <name> --all        # → wick-plugins/bin/*.zip
gh release create "<name>/v$(cat connector/<name>/VERSION)" bin/<name>-*.zip
# then either edit plugins.json by hand, or re-run the update-catalog job.
```

## Constraints (enforced, not just advised)

- **`Meta.Key` MUST equal the folder name.** Key is the one identity — source
  folder, zip name, install dir (`~/.wick/plugins/connectors/<key>`), runtime
  registry key, and catalog match all use it. `wick plugin build` fails if they
  differ. `Meta.Name` is the free display string (spaces/caps OK).
- **`key` must be a slug: lowercase `a-z`, digits, `_` only.** No `-`, spaces,
  slashes, or dots — enforced by `ValidateKey` at BOTH build and install time
  (`-` would break the `<key>-<ver>-<os>-<arch>.zip` split; `/`/`.` would let the
  install dir escape the plugins dir). Multi-word → `google_workspace`, not
  `google-workspace`.
- **`VERSION` is the source of truth.** The release tag, zip names, and catalog
  entry all read it. Bump it for every release.
- **Signing is optional.** Add `--sign-key <ed25519>` (manifest) and/or
  `--cosign-key <key>` (binary, external cosign CLI) to the build step. The host
  rejects a tampered binary regardless (sha256 in the manifest is always checked).

## When wick-plugins is extracted to its own repo

Move `.github/workflows/release-plugins.yml` into that repo and drop the
`wick-plugins/` path prefixes (paths become `connector/**`, working-dir becomes
`.`). Replace "build wick CLI from source" with
`go install github.com/yogasw/wick@<version>` once a wick release contains
`pkg/plugin`.
