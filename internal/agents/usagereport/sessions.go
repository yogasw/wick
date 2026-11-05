package usagereport

import (
	"sort"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// Sort orders for Builder.Sessions.
const (
	SortTokens = "tokens" // most tokens first (the default)
	SortCost   = "cost"   // highest cost first
	SortTurns  = "turns"  // most turns first
	SortRecent = "recent" // most recently used first
)

// SessionsReport answers "which conversations spent it": every session in
// the window and scope, one row each, across all providers it ran on.
type SessionsReport struct {
	Window      string `json:"window"`
	WindowLabel string `json:"window_label"`
	Since       string `json:"since,omitempty"`
	Until       string `json:"until,omitempty"`
	Sort        string `json:"sort"`

	Totals Totals `json:"totals"`
	Turns  int    `json:"turns"`
	// Total is how many sessions matched; Sessions holds at most the limit
	// asked for, so a caller can say "top 20 of 263".
	Total    int          `json:"total"`
	Sessions []SessionRow `json:"sessions"`

	Partial bool   `json:"partial,omitempty"`
	Note    string `json:"note,omitempty"`
}

// SessionRow is one conversation's spend in the window. Share is its
// percentage of the report total, the number that says "this one session
// is a third of the bill".
type SessionRow struct {
	SessionUse
	Channel   string   `json:"channel,omitempty"`
	Agent     string   `json:"agent,omitempty"`
	Providers []string `json:"providers,omitempty"`
	Share     float64  `json:"share"`
}

// Sessions lists the conversations of a window, merged across providers
// and ordered by sortBy (SortTokens when empty or unknown). limit <= 0
// returns every row.
func (b *Builder) Sessions(w Window, sc Scope, force bool, sortBy string, limit int) (SessionsReport, error) {
	roll, err := b.Rollup(w, sc, force)
	if err != nil {
		return SessionsReport{}, err
	}
	switch sortBy {
	case SortTokens, SortCost, SortTurns, SortRecent:
	default:
		sortBy = SortTokens
	}

	type acc struct {
		totals    store.UsageTotals
		turns     int
		lastAt    time.Time
		providers []string
	}
	byID := map[string]*acc{}
	for provider, uses := range roll.ProviderSessions {
		for _, use := range uses {
			a := byID[use.ID]
			if a == nil {
				a = &acc{}
				byID[use.ID] = a
			}
			a.totals.Add(use.Totals)
			a.turns += use.Turns
			if use.LastAt.After(a.lastAt) {
				a.lastAt = use.LastAt
			}
			a.providers = append(a.providers, provider)
		}
	}

	grand := TotalsOf(roll.Totals)
	rows := make([]SessionRow, 0, len(byID))
	for id, a := range byID {
		t := roll.SessionTagsByID[id]
		sort.Strings(a.providers)
		row := SessionRow{
			SessionUse: SessionUse{
				ID:        id,
				Label:     t.Label,
				ProjectID: t.ProjectID,
				UserID:    t.UserID,
				Turns:     a.turns,
				Totals:    TotalsOf(a.totals),
			},
			Channel:   t.Channel,
			Agent:     t.Agent,
			Providers: a.providers,
		}
		if t.ProjectID != "" {
			row.ProjectName = b.projectName(t.ProjectID)
		}
		if t.UserID != "" {
			row.UserName = b.userName(t.UserID)
		}
		if !a.lastAt.IsZero() {
			row.LastAt = a.lastAt.UTC().Format(time.RFC3339)
		}
		if grand.Total > 0 {
			row.Share = float64(row.Totals.Total) / float64(grand.Total) * 100
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch sortBy {
		case SortCost:
			if a.Totals.CostUSD != b.Totals.CostUSD {
				return a.Totals.CostUSD > b.Totals.CostUSD
			}
		case SortTurns:
			if a.Turns != b.Turns {
				return a.Turns > b.Turns
			}
		case SortRecent:
			if a.LastAt != b.LastAt {
				return a.LastAt > b.LastAt // RFC3339 UTC sorts as text
			}
		}
		if a.Totals.Total != b.Totals.Total {
			return a.Totals.Total > b.Totals.Total
		}
		return a.ID < b.ID
	})

	out := SessionsReport{
		Window:      w.Key,
		WindowLabel: w.Label,
		Sort:        sortBy,
		Totals:      grand,
		Turns:       roll.Turns,
		Total:       len(rows),
		Partial:     roll.Partial,
	}
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	out.Sessions = rows
	if !w.Since.IsZero() {
		out.Since = w.Since.UTC().Format(time.RFC3339)
	}
	if !w.Until.IsZero() {
		out.Until = w.Until.UTC().Format(time.RFC3339)
	}
	if roll.Partial {
		out.Note = partialNote
	}
	return out, nil
}
