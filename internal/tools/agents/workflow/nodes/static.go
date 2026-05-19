package nodes

import "embed"

// StaticFS embeds every per-node JS module. Glob "all:<subfolder>"
// covers any current or future <type>/inspector.js — drop a new
// subfolder under this package, no embed directive update needed.
//
// Mount via Router.Static("/static/nodes/", StaticFS) in
// internal/tools/agents/handler.go so the editor can
// `<script src="/static/nodes/<type>/inspector.js">`.
//
//go:embed all:go_script all:http all:session_init all:switchnode
var StaticFS embed.FS
