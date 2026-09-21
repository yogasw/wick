package admin

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/usagereport"
)

// analytics_ledger.go serves the token ledger to the admin analytics
// page — the same report the providers page reads, from the same
// builder in internal/agents/usagereport.
//
// Why it is served here at all, rather than pointing the page at the
// agents tool: that tool is mounted at a path only it knows, and
// reaching across would mean the admin page guessing a URL and a grant
// it does not own. The report is cheap to build and already cached, so
// the honest arrangement is one report, two mounts.
//
// This is also what makes the providers page disposable: whoever removes
// the per-provider card later takes nothing away from the admin, because
// the numbers were never living in that component.

var adminLedger struct {
	mu sync.Mutex
	b  *usagereport.Builder
}

func (h *Handler) ledger() *usagereport.Builder {
	adminLedger.mu.Lock()
	defer adminLedger.mu.Unlock()
	if adminLedger.b == nil || adminLedger.b.Layout != agentsLayout {
		adminLedger.b = usagereport.New(agentsLayout, h.userDisplayName)
	}
	return adminLedger.b
}

// userDisplayName resolves a wick user id to the name the rest of the
// admin shows.
//
// Cached for a few seconds because it is called once per row: a report
// with forty people in it would otherwise be forty round trips to the
// accounts table to answer one page. Nothing here is fatal — a row with
// no name still carries its id, which is the truth about who spent it.
var ledgerNames struct {
	mu    sync.Mutex
	at    time.Time
	names map[string]string
}

const ledgerNamesTTL = 30 * time.Second

func (h *Handler) userDisplayName(id string) string {
	if id == "" || h.repo == nil {
		return ""
	}
	ledgerNames.mu.Lock()
	defer ledgerNames.mu.Unlock()
	if ledgerNames.names == nil || time.Since(ledgerNames.at) > ledgerNamesTTL {
		users, err := h.repo.ListUsers(context.Background())
		if err != nil {
			return ""
		}
		m := make(map[string]string, len(users))
		for _, u := range users {
			switch {
			case u.Name != "":
				m[u.ID] = u.Name
			case u.Email != "":
				m[u.ID] = u.Email
			}
		}
		ledgerNames.names, ledgerNames.at = m, time.Now()
	}
	return ledgerNames.names[id]
}

// analyticsLedgerJSON serves GET /admin/analytics/ledger/usage and
// /admin/analytics/ledger/{type}/{name}/usage — the fleet report and one
// provider's slice of it, in the shape the shared UsageReport component
// reads.
func (h *Handler) analyticsLedgerJSON(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	win := usagereport.ParseWindow(q.Get("window"), time.Now())
	force := q.Get("refresh") == "1"

	// Everything after /ledger/ is either "usage" (the fleet) or
	// "<type>/<name>/usage" (one provider).
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/analytics/ledger"), "/")
	if rest == "usage" || rest == "" {
		rep, err := h.ledger().Report(win, force)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, rep)
		return
	}
	key := strings.TrimSuffix(rest, "/usage")
	if key == rest || strings.Count(key, "/") != 1 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown ledger path"})
		return
	}
	rep, err := h.ledger().Provider(key, win, force)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
