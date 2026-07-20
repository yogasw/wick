// Command notion is the official Notion connector shipped as a wick plugin. It
// talks to the public Notion REST API (https://api.notion.com/v1) using an
// Internal Integration bot token — no OAuth. Where the Notion MCP returns
// ready-to-read markdown from a single "fetch", this connector rebuilds that
// convenience deterministically on top of REST: it flattens page properties and
// walks the block tree into markdown (see repo.go), so the agent gets an
// MCP-style result without any AI in the loop.
//
// main() stays tiny — the connector definition lives in connector.go's
// Module(). wickplugin.Serve turns it into a gRPC plugin binary the wick host
// spawns on demand (and answers --dump-manifest at build time).
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.Serve(Module())
}
