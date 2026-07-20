// Command loki is the Grafana Loki connector shipped as a wick plugin. It
// queries logs and discovers labels via LogQL against a Loki instance behind
// Grafana's datasource-proxy API.
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
