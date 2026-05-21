// Package all blank-imports every node-type subpackage so their
// init() registrations land in the parent nodes registry. Mount this
// package from tools/agents server bootstrap (workflows.go) — one
// import covers them all.
package all

import (
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/branch"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/classify"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/datatable"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/db_query"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/end"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/go_script"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/http"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/session_init"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/shell"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/switchnode"
	_ "github.com/yogasw/wick/internal/tools/agents/workflow/nodes/transform"
)
