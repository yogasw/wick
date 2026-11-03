// Command codemode_goja is the JavaScript/TypeScript runner behind wick's
// Code Mode (wick_run_code). wick sends it a script over the plugin socket;
// every run gets a fresh goja VM on its own goroutine. The script reaches
// connectors only through wick.call / wick.callMany, which the runner hands
// back to wick as "call" events: wick executes them as the calling session
// and posts the result back. The runner never holds a credential.
//
// Endpoints live under /_wick/codemode/, which wick refuses from outside, so
// only wick itself (through the socket) can reach them.
package main

import "github.com/yogasw/wick/pkg/service"

func main() {
	service.ServeService(Module())
}
