# service/_template

Starter for a service plugin (`service.ServeService`): always on, exposed at
`/x/<key>/*` with per-route auth. Copy to `service/<key>/` (`_` in keys, never
`-`), edit `service.go`, bump `VERSION`, then
`wick plugin build --kind service <key>`. Docs:
`docs/plugins/authoring-service.md`.
