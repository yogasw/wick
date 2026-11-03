package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/service"
)

const (
	cfgBudgetMB = "memory_budget_mb"
	// cfgMaxRSSMB is read by wick's supervisor: past it the process is
	// restarted, whatever the script did.
	cfgMaxRSSMB = "wick_max_rss_mb"

	defaultBudgetMB = 1024
)

// Module describes the runner service. Paths are relative to /x/codemode_goja.
func Module() service.Module {
	var r *runner
	return service.Module{
		Meta: service.Meta{
			Key:         "codemode_goja",
			Name:        "Code Mode runner (JS/TS)",
			Description: "Runs wick_run_code scripts in JavaScript or TypeScript on goja. Scripts reach connectors only through wick; this process holds no credential.",
			Icon:        "🧪",
		},
		Routes: []service.Route{{Prefix: "/status", Auth: service.Session}},
		Configs: []entity.Config{
			{Key: cfgBudgetMB, Value: strconv.Itoa(defaultBudgetMB), Description: "Heap the runner may use for scripts (MB). New runs wait while usage is above 80% of it; past 100% the running scripts are stopped with \"out of memory\"."},
			{Key: cfgMaxRSSMB, Value: "1536", Description: "Hard limit (MB of resident memory). wick restarts the runner past it."},
		},
		AutoOff: service.AutoOff{Supported: true, DefaultIdle: 30 * time.Minute},
		Register: func(mux *http.ServeMux, env *service.Env) {
			r = newRunner(func() int {
				if n, err := strconv.Atoi(env.Cfg(cfgBudgetMB)); err == nil && n > 0 {
					return n
				}
				return defaultBudgetMB
			})
			r.mount(mux)
		},
	}
}
