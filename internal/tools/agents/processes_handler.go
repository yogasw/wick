package agents

import (
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/pkg/memreport"
	"github.com/yogasw/wick/internal/pkg/sysmem"
	"github.com/yogasw/wick/pkg/tool"
)

// processes_handler.go serves the searchable, paginated process explorer.
//
// Deliberately a separate endpoint from /api/memory: that one is polled
// every 10 seconds by an open page, and shipping all ~350 processes on
// every poll would be most of the payload for a table the operator is
// usually not looking at. This one is fetched only when the explorer is
// actually used.

// processGroupRow is one executable name with its processes summed.
type processGroupRow struct {
	Name  string `json:"name"`
	Count int    `json:"count"`

	RSSBytes   uint64  `json:"rss_bytes"`
	CPUPct     float64 `json:"cpu_pct"`
	IOReadBps  uint64  `json:"io_read_bps"`
	IOWriteBps uint64  `json:"io_write_bps"`
	// PctOfMachineMem answers "is that a lot?", which a byte count alone
	// cannot: 2.1 GB means one thing on a 4 GB box and another on 64.
	PctOfMachineMem float64 `json:"pct_of_machine_mem"`

	// Members are the individual processes, heaviest first, so the UI can
	// expand a group without a second request.
	Members []topProcessRow `json:"members"`
}

// processListResponse is the payload behind GET /api/processes.
type processListResponse struct {
	Available bool `json:"available"`
	// Total is every process on the machine; Matched is how many survived
	// the search. Both are reported so the UI can say "12 of 338" rather
	// than leaving the operator wondering what was filtered out.
	Total   int `json:"total"`
	Matched int `json:"matched"`
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
	Pages   int `json:"pages"`

	MachineMemBytes uint64 `json:"machine_mem_bytes"`
	// CPUCores is what makes a CPU percentage readable. These are percent
	// of ONE core, so a busy browser legitimately reads 444% on a 16-core
	// box — a number that looks like a bug until you know the ceiling is
	// 1600%, not 100%.
	CPUCores int               `json:"cpu_cores"`
	Groups   []processGroupRow `json:"groups"`

	// SelfPID is this wick server. The kill handler refuses it, and the UI
	// needs to know which row that is so it can say so up front — a row
	// that looks exactly like every other one but silently declines to die
	// reads as a broken button, not as a safety rule.
	SelfPID int `json:"self_pid"`
}

// defaultPerPage keeps a page small enough to read. The operator paginates
// or searches rather than scrolling 300 rows.
const defaultPerPage = 20

// processesHandler serves the process explorer: grouped by executable,
// searchable, paginated, sortable.
//
// Query parameters:
//
//	q         substring match on the executable name (case-insensitive)
//	sort      mem | cpu | io          (default mem)
//	page      1-based                 (default 1)
//	per_page  bounded 1..100          (default 20)
func processesHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}

	procs, err := memreport.Snapshot()
	if err != nil {
		// Not an error state: a platform without a process listing is a
		// supported configuration, and the UI renders a notice from this.
		c.JSON(http.StatusOK, processListResponse{Available: false})
		return
	}

	// Share the rate tracker with the dashboard so both views agree on
	// what a process is doing right now, and so opening the explorer does
	// not reset the rates the dashboard is showing.
	topRatesMu.Lock()
	rateList := topRates.Update(time.Now(), procs)
	topRatesMu.Unlock()

	machineMem, _ := sysmem.Total()
	groups := memreport.GroupBy(rateList, machineMem)

	// Search before ranking, so a filtered view is ranked among its own
	// matches rather than showing the tail of a global ranking.
	if q := strings.TrimSpace(c.Query("q")); q != "" {
		needle := strings.ToLower(q)
		filtered := groups[:0:0]
		for _, g := range groups {
			if groupMatches(g, needle) {
				filtered = append(filtered, g)
			}
		}
		groups = filtered
	}

	key := memreport.GroupByMem
	switch c.Query("sort") {
	case "cpu":
		key = memreport.GroupByCPU
	case "io":
		key = memreport.GroupByIO
	}
	// 0 = no cap: the explorer pages through everything rather than
	// showing a top-N, which is the whole difference from the dashboard.
	// Sorting without the zero-tail drop, because a search for an idle
	// process should still find it.
	sortGroups(groups, key)

	perPage := defaultPerPage
	if n, err := strconv.Atoi(c.Query("per_page")); err == nil && n > 0 {
		perPage = min(n, 100)
	}
	page := 1
	if n, err := strconv.Atoi(c.Query("page")); err == nil && n > 0 {
		page = n
	}

	matched := len(groups)
	pages := (matched + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	if page > pages {
		page = pages
	}

	start := (page - 1) * perPage
	end := min(start+perPage, matched)
	if start > matched {
		start = matched
	}

	c.JSON(http.StatusOK, processListResponse{
		Available:       true,
		Total:           len(procs),
		Matched:         matched,
		Page:            page,
		PerPage:         perPage,
		Pages:           pages,
		MachineMemBytes: machineMem,
		CPUCores:        runtime.NumCPU(),
		Groups:          toGroupRows(groups[start:end]),
		SelfPID:         os.Getpid(),
	})
}

// groupMatches reports whether a search term hits this group, by name or
// by any member's command line.
//
// Searching the command matters more than searching the name: the rows an
// operator most needs to find are the ambiguous ones — several "node" or
// "python3" processes where only the arguments say which is which.
// Matching on name alone would return all of them or none.
//
// needle must already be lower-cased by the caller, so the comparison is
// not redone per member.
func groupMatches(g memreport.ProcGroup, needle string) bool {
	if strings.Contains(strings.ToLower(g.Name), needle) {
		return true
	}
	for _, m := range g.Members {
		if m.Cmdline != "" && strings.Contains(strings.ToLower(m.Cmdline), needle) {
			return true
		}
	}
	return false
}

// sortGroups ranks in place, ties on name so paging is stable — a row
// must not move to another page between requests.
func sortGroups(groups []memreport.ProcGroup, key func(memreport.ProcGroup) float64) {
	sort.Slice(groups, func(i, j int) bool {
		a, b := key(groups[i]), key(groups[j])
		if a != b {
			return a > b
		}
		return groups[i].Name < groups[j].Name
	})
}

func toGroupRows(in []memreport.ProcGroup) []processGroupRow {
	out := make([]processGroupRow, 0, len(in))
	for _, g := range in {
		row := processGroupRow{
			Name:            g.Name,
			Count:           g.Count,
			RSSBytes:        g.RSSBytes,
			CPUPct:          g.CPUPct,
			IOReadBps:       g.IOReadBps,
			IOWriteBps:      g.IOWriteBps,
			PctOfMachineMem: g.PctOfMachineMem,
		}
		// Cap the expandable members: a browser with 40 renderers would
		// otherwise carry 40 rows per group across the whole page.
		row.Members = toTopRows(capMembers(g.Members, maxProcessRows))
		out = append(out, row)
	}
	return out
}

func capMembers(in []memreport.ProcRate, limit int) []memreport.ProcRate {
	if limit > 0 && len(in) > limit {
		return in[:limit]
	}
	return in
}
