// Command source_deploy updates the wick it is installed in from source — a branch, a
// fork or a local checkout — by running the deploy-from-source script in the background.
// A plugin rather than part of wick on purpose: it changes far more often than wick is
// released, and a fix to it is a plugin update, not a release.
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	// Runs and profiles live in the plugin's data dir.
	wickplugin.DeclareHostAccess([]string{wickplugin.ScopeHostDatadir}, nil)
	wickplugin.Serve(Module())
}
