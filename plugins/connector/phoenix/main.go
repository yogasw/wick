// Command phoenix is the Arize Phoenix observability connector shipped as a
// wick plugin. It inspects LLM spans read-only — listing spans by conversation
// room or app_id and drilling into a single span's messages, tool calls, and
// token usage — to answer "why did the agent answer X" without mutating any
// Phoenix data.
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
