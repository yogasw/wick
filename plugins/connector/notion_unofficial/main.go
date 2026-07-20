// Command notion_unofficial is the UNOFFICIAL Notion connector shipped as a wick
// plugin. Unlike the official `notion` connector (public REST API + bot token),
// this one drives Notion's private web API (api/v3: loadPageChunk,
// queryCollection, submitTransaction, …) via a small hand-rolled client (see
// client.go), using a browser session cookie (token_v2). We parse the raw
// api/v3 recordMap ourselves rather than depend on kjk/notionapi, which can no
// longer decode Notion's current response shape (numeric __version__ +
// value.value nesting).
//
// Trade-offs vs official:
//   - Auth is a browser cookie, not an integration token — it can expire and it
//     inherits the full access of the logged-in user (no per-page sharing step).
//   - The private API is undocumented and can change without notice; it is
//     "mostly read, limited write". Treat this connector as best-effort.
//   - Upside: fetch returns rich MCP-style markdown for free (kjk's tomarkdown),
//     and it sees everything the user can see without sharing pages first.
//
// main() stays tiny — the connector definition lives in connector.go's Module().
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.Serve(Module())
}
