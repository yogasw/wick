# Authoring a connector plugin

A connector plugin is the same `connector.Module` a built-in connector uses,
served over gRPC by `wickplugin.Serve`. The deeper reference for the module
itself is [Connector plugins](../guide/connector-plugins.md) and
[Connector module](../guide/connector-module.md).

## Start from the template

```bash
cd plugins                      # the folder holding connector/ tool/ job/ service/
cp -r connector/_template connector/<key>
echo 0.1.0 > connector/<key>/VERSION
```

- `main.go` calls `wickplugin.Serve(Module())` — leave it.
- `connector.go` holds `Meta` (`Key` = folder name, `_` not `-`), `Configs`
  (credentials, encrypted by wick) and the operations.

## Build and inspect

```bash
wick plugin build --kind connector <key> --target linux/amd64
# → bin/<key>-0.1.0-linux-amd64.zip  { <key>, plugin.json }

go build -o /tmp/<key> ./connector/<key> && /tmp/<key> --dump-manifest
```

`--dump-manifest` prints the `plugin.json` that `plugin build` puts in the
zip; check `module.meta.key`, the operations and configs there.

## Test locally

```bash
<app> plugin install bin/<key>-0.1.0-linux-amd64.zip   # or a folder / URL
<app> plugin list
```

or upload the zip from **Admin → Plugins → Add new plugin → Upload**. The
connector then appears in `wick_list` like a built-in one.

## Release

Bump `VERSION` and follow [Release](./release.md).
