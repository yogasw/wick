// Command _template is the starter for a new wick connector plugin.
//
// To make your own connector:
//  1. Copy this folder:  cp -r connector/_template connector/<your-name>
//  2. Edit connector.go — change Meta.Key/Name and the operations.
//  3. Set VERSION (e.g. 0.1.0).
//  4. Build:   wick plugin build <your-name> --target linux/arm64
//
// main() is intentionally tiny — all the connector definition lives in
// connector.go's Module(). wickplugin.Serve turns it into a gRPC plugin binary
// that the wick host spawns on demand (and that responds to --dump-manifest at
// build time).
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.Serve(Module())
}
