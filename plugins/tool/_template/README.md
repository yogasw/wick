# tool/_template

Starter for a tool plugin (`toolplugin.ServeTool`), served at `/tools/<key>`.
Copy to `tool/<key>/` (`_` in keys, never `-`), edit `tool.go`, bump
`VERSION`, then `wick plugin build --kind tool <key>`. Docs:
`docs/plugins/authoring-tool.md`.
