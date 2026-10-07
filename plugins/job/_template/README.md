# job/_template

Starter for a job plugin (`wickplugin.ServeJob`): spawned per run, killed when
`Run` returns. Copy to `job/<key>/` (`_` in keys, never `-`), edit `job.go`,
bump `VERSION`, then `wick plugin build --kind job <key>`. Docs:
`docs/plugins/authoring-job.md`.
