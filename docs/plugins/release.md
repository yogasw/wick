# Releasing plugins

Two pipelines share the same tools (`wick plugin build` → `wick plugin index`
→ GitHub release `<key>/v<version>` with the zips and `plugins.json`):

- **Plugins inside the wick repo** (`plugins/`): `release-plugins.yml`,
  described in [`plugins/RELEASE.md`](https://github.com/yogasw/wick/blob/master/plugins/RELEASE.md).
  It also regenerates the in-app catalog.
- **Your own plugins repo** (public or private): call the reusable workflow.

## Your own repo

Layout — one Go module, one folder per plugin:

```
go.mod
plugins/
├── tool/<key>/{main.go, VERSION}
├── job/<key>/{main.go, VERSION}
├── service/<key>/{main.go, VERSION}
└── connector/<key>/{main.go, VERSION}
```

`.github/workflows/release.yml`:

```yaml
name: Release plugins
on:
  push:
    branches: [main]
    paths: ["plugins/**", "go.mod", "go.sum"]
  workflow_dispatch:
jobs:
  release:
    uses: yogasw/wick/.github/workflows/plugin-release.yml@master
    with:
      working-directory: plugins
      targets: linux/amd64,linux/arm64
      # go-private: github.com/your-org/*   # private Go dependencies
    secrets: inherit   # PLUGIN_SIGNING_KEY, GO_PRIVATE_TOKEN (both optional)
```

Inputs of `plugin-release.yml`:

| Input | Default | |
|---|---|---|
| `working-directory` | `plugins` | folder holding `<kind>/<key>/` |
| `targets` | `linux/amd64,linux/arm64` | comma list, or `all` |
| `plugins` | empty | only `"<kind>/<key>,..."` |
| `force` | `false` | re-release an existing tag (replaces assets) |
| `wick-ref` | `master` | wick ref the CLI is built from |
| `go-private` | empty | `GOPRIVATE` pattern |

Secrets: `PLUGIN_SIGNING_KEY` (base64 ed25519 private key; signs manifests and
`zip_sha256`) and `GO_PRIVATE_TOKEN` (read access to private modules).

## Releasing

1. Change the plugin, bump `plugins/<kind>/<key>/VERSION`.
2. Push to `main`. Every plugin whose `<key>/v<VERSION>` tag does not exist
   gets one release; others are skipped.
3. wick hosts with a GitHub source on the repo see the new version within the
   poll interval (or on *Check now*) and offer *Update to vX*.

Release builds never take the GitHub "Latest" badge (`make_latest: false`).

## Signing

```bash
go run github.com/yogasw/wick/cmd/plugin-keygen ~/.wick/plugin-signing.key
# prints the public key → pin it on the source in Admin → Plugins → Sources
```

Store the private key as the `PLUGIN_SIGNING_KEY` secret. Hosts that pin the
public key reject unsigned or wrongly signed releases.

## Notes

- Pushing files under `.github/workflows/` needs a token with the `workflow`
  scope.
- The plugins repo must depend on a released `github.com/yogasw/wick` that
  contains the plugin SDK (`pkg/plugin`, `pkg/plugin/toolplugin`,
  `pkg/service`); local `replace` lines do not work in CI.
