// Command gitlab is the wick GitLab connector shipped as an external plugin.
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.Serve(Module())
}
