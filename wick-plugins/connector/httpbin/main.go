// Command httpbin is a real, working sample connector for the wick-plugins
// monorepo. It wraps httpbin.org (a free public HTTP request/response testing
// service) so you can install it, run it, and see the whole plugin flow work
// end-to-end without any credentials.
//
// Use it as a reference next to connector/_template/ — _template shows the
// minimal shape, httpbin shows a complete connector you can actually exercise.
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.Serve(Module())
}
