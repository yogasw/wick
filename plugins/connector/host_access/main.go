// Command host_access exposes explicitly authorized local filesystem and
// terminal operations as an optional Wick connector plugin.
//
// The plugin is intentionally separate from Wick core: installations that
// do not need host access should not have to load its code or operations.
// Module declares one fixed instance with a default-deny JSON policy.
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	// Serve handles both manifest generation and the runtime gRPC protocol.
	// Host API scopes are not needed: all operations use local OS facilities.
	wickplugin.Serve(Module())
}
