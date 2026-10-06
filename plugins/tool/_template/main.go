// Command _template is the starter for a new wick tool plugin.
//
// To make your own tool:
//  1. Copy this folder:  cp -r tool/_template tool/<your-name>
//  2. Edit tool.go — change Meta.Key/Name/Icon, Config, and the routes.
//  3. Set VERSION (e.g. 0.1.0).
//  4. Build:   wick plugin build --kind tool <your-name> --target linux/arm64
//
// toolplugin.ServeTool runs the same tool.Module a built-in tool uses. The
// wick host spawns the binary on the first request to /tools/{key}, proxies
// every request to it over a unix socket, wraps c.HTML pages in wick's
// layout, and stops it after it has been idle for a while. Pass
// toolplugin.KeepWarm() when a webhook must answer within a few seconds.
// --dump-manifest prints the kind=tool plugin.json at build time.
package main

import "github.com/yogasw/wick/pkg/plugin/toolplugin"

func main() {
	toolplugin.ServeTool(Module())
}
