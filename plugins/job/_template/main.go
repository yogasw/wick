// Command _template is the starter for a new wick job plugin.
//
// To make your own job:
//  1. Copy this folder:  cp -r job/_template job/<your-name>
//  2. Edit job.go — change Meta.Key/Name/DefaultCron, Config and Run.
//  3. Set VERSION (e.g. 0.1.0).
//  4. Build:   wick plugin build --kind job <your-name> --target linux/arm64
//
// wickplugin.ServeJob turns the module into a gRPC job plugin. The wick host
// spawns it when the job fires (cron or Run now), calls Run once, and kills
// the process as soon as Run returns. --dump-manifest prints the kind=job
// plugin.json at build time.
package main

import (
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	wickplugin.ServeJob(Module())
}
