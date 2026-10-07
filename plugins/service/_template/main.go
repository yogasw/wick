// Command _template is the starter for a new wick service plugin.
//
// To make your own service:
//  1. Copy this folder:  cp -r service/_template service/<your_name>
//  2. Edit service.go — change Meta.Key/Name, Routes, and the handlers.
//  3. Set VERSION (e.g. 0.1.0).
//  4. Build:   wick plugin build --kind service <your_name> --target linux/arm64
//
// A service plugin is always on: wick starts it at boot, restarts it with
// backoff (1s→30s) when it crashes, and proxies /x/{key}/* to it over a unix
// socket. Each Route names who may reach a path prefix:
//
//	service.Public  — anyone (webhooks, A2A agent cards)
//	service.Token   — "Authorization: Bearer <token>" an admin generated on
//	                  the plugin's page (wick checks it; the plugin never
//	                  sees it)
//	service.Session — a signed-in wick user (X-Wick-User-* headers)
//
// env.Callback() returns WICK_BASE_URL and WICK_PLUGIN_TOKEN for calling wick
// back within CallbackScopes. Never log the token. Set Module.RemoteSource to
// make the plugin a Team remote agent source ("Plugin" in the wizard).
// --dump-manifest prints the kind=service plugin.json at build time.
package main

import "github.com/yogasw/wick/pkg/service"

func main() {
	service.ServeService(Module())
}
