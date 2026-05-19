package workflow

import (
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow/guard"
)

// runsLabel formats the runs-count chip in the bottom-tab strip.
func runsLabel(n int) string { return fmt.Sprintf("%d", n) }

// guardReportLike is an alias re-export so editor_bottom.templ doesn't
// need to import the guard package directly — keeps every templ file
// to one or zero external imports.
type guardReportLike = guard.Report

// runStatusClass picks the colour class for the status pill in the
// run-detail header.
func runStatusClass(status string) string {
	switch status {
	case "success":
		return "text-emerald-700 font-semibold"
	case "failed":
		return "text-red-700 font-semibold"
	case "running":
		return "text-amber-700 font-semibold"
	}
	return "text-slate-700"
}
