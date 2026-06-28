// Command bitbucket is the wick Bitbucket connector shipped as an external
// plugin. main() is intentionally tiny — the whole connector lives in
// connector.go's Module(). wickplugin.Serve turns it into a gRPC plugin binary
// the wick host spawns on demand (and that answers --dump-manifest at build time).
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.Serve(Module())
}
