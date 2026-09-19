package agents

import (
	"net/http"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/pkg/tool"
)

// api_usage.go serves the token ledger: what each provider burned, which
// projects and people it was burned for, and where a given provider is
// actually being used.
//
// The numbers come from per-session usage.json files written by the
// store. Rolling them up means reading one small file per session, and
// there are thousands of sessions on a busy host — so the roll-up is
// cached for a few seconds. Usage is an analytics view, not a control
// surface: a reading that is ten seconds stale costs nobody anything,
// while re-walking the whole session tree on every poll would show up as
// disk churn on a 2-vCPU box.

// usageRollupTTL is how long a computed roll-up is reused. Short enough
// that a finished turn shows up while the user is still looking at the
// page, long enough that a dashboard polling every second does not walk
// the session tree every second.
const usageRollupTTL = 15 * time.Second

var tokenLedgerCache struct {
	mu   sync.Mutex
	at   time.Time
	roll store.UsageRollup
}

// loadUsageRollup returns the cached roll-up, recomputing when stale.
func loadUsageRollup(layout config.Layout, force bool) (store.UsageRollup, error) {
	tokenLedgerCache.mu.Lock()
	defer tokenLedgerCache.mu.Unlock()
	if !force && time.Since(tokenLedgerCache.at) < usageRollupTTL && tokenLedgerCache.at != (time.Time{}) {
		return tokenLedgerCache.roll, nil
	}
	ids, err := session.ListAll(layout)
	if err != nil {
		return store.UsageRollup{}, err
	}
	roll, err := store.AggregateUsage(layout, ids, func(id string) store.SessionTags {
		s, err := session.Load(layout, id)
		if err != nil {
			return store.SessionTags{}
		}
		return store.SessionTags{ProjectID: s.Meta.ProjectID, UserID: s.Meta.UserID}
	})
	if err != nil {
		return store.UsageRollup{}, err
	}
	tokenLedgerCache.at = time.Now()
	tokenLedgerCache.roll = roll
	return roll, nil
}

// UsageTotalsDTO mirrors store.UsageTotals plus the derived numbers a UI
// would otherwise recompute in three places.
type UsageTotalsDTO struct {
	Input      int     `json:"input"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Output     int     `json:"output"`
	Total      int     `json:"total"`
	CostUSD    float64 `json:"cost_usd"`
	// CacheHitPct is cache reads as a share of all input tokens — the
	// single most actionable number here, because a collapsing hit rate
	// is what turns a cheap session expensive.
	CacheHitPct float64 `json:"cache_hit_pct"`
}

func usageTotalsDTO(t store.UsageTotals) UsageTotalsDTO {
	in := t.Input + t.CacheRead + t.CacheWrite
	d := UsageTotalsDTO{
		Input:      t.Input,
		CacheRead:  t.CacheRead,
		CacheWrite: t.CacheWrite,
		Output:     t.Output,
		Total:      in + t.Output,
		CostUSD:    t.CostUSD,
	}
	if in > 0 {
		d.CacheHitPct = float64(t.CacheRead) / float64(in) * 100
	}
	return d
}

// UsageReportDTO is the whole-fleet answer: totals, and the same totals
// sliced by provider, project and user.
type UsageReportDTO struct {
	Totals   UsageTotalsDTO `json:"totals"`
	Turns    int            `json:"turns"`
	Sessions int            `json:"sessions"`

	ByProvider []UsageSliceDTO `json:"by_provider"`
	ByProject  []UsageSliceDTO `json:"by_project"`
	ByUser     []UsageSliceDTO `json:"by_user"`
}

// UsageSliceDTO is one row of a breakdown, sorted by cost then tokens so
// the expensive row is always first.
type UsageSliceDTO struct {
	Key      string         `json:"key"`
	Label    string         `json:"label,omitempty"`
	Totals   UsageTotalsDTO `json:"totals"`
	Sessions int            `json:"sessions,omitempty"`
	// Share is this row's percentage of the report total, so a bar can be
	// drawn without the client summing the list first.
	Share float64 `json:"share"`
}

func usageSlices(m map[string]store.UsageTotals, grand int) []UsageSliceDTO {
	out := make([]UsageSliceDTO, 0, len(m))
	for k, v := range m {
		d := usageTotalsDTO(v)
		row := UsageSliceDTO{Key: k, Totals: d}
		if grand > 0 {
			row.Share = float64(d.Total) / float64(grand) * 100
		}
		out = append(out, row)
	}
	sortUsageSlices(out)
	return out
}

// sortUsageSlices orders by cost, then by tokens, then by key — cost
// first because that is the question being asked, key last so the order
// is stable when two rows tie (a list that reshuffles between polls is
// unreadable).
func sortUsageSlices(rows []UsageSliceDTO) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			less := b.Totals.CostUSD > a.Totals.CostUSD ||
				(b.Totals.CostUSD == a.Totals.CostUSD && b.Totals.Total > a.Totals.Total) ||
				(b.Totals.CostUSD == a.Totals.CostUSD && b.Totals.Total == a.Totals.Total && b.Key < a.Key)
			if !less {
				break
			}
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// apiUsageReport serves GET /api/providers/usage — the fleet-wide report.
func apiUsageReport(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireProviderMenu(c) {
		return
	}
	layout := globalLayout
	roll, err := loadUsageRollup(layout, c.Query("refresh") == "1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	grand := usageTotalsDTO(roll.Totals).Total
	rep := UsageReportDTO{
		Totals:     usageTotalsDTO(roll.Totals),
		Turns:      roll.Turns,
		Sessions:   roll.Sessions,
		ByProvider: usageSlices(roll.ByProvider, grand),
		ByProject:  usageSlices(roll.ByProject, grand),
		ByUser:     usageSlices(roll.ByUser, grand),
	}
	for i, row := range rep.ByProvider {
		rep.ByProvider[i].Sessions = len(roll.ProviderSessions[row.Key])
	}
	c.JSON(http.StatusOK, rep)
}

// ProviderUsageDetailDTO answers "where is provider A used, and what did
// it cost" for one provider.
type ProviderUsageDetailDTO struct {
	Provider string         `json:"provider"`
	Totals   UsageTotalsDTO `json:"totals"`
	Sessions []string       `json:"sessions"`
}

// apiProviderUsage serves GET /api/providers/{type}/{name}/usage.
func apiProviderUsage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireProviderMenu(c) {
		return
	}
	key := c.PathValue("type") + "/" + c.PathValue("name")
	roll, err := loadUsageRollup(globalLayout, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ProviderUsageDetailDTO{
		Provider: key,
		Totals:   usageTotalsDTO(roll.ByProvider[key]),
		Sessions: roll.ProviderSessions[key],
	})
}
