package admin

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/usagereport"
)

// analytics_mcp.go is the People & usage page for an agent: the same
// builders the page and its ledger card read, reached without a browser
// session. The wick manager connector calls these; the admin gate lives
// there, so nothing here checks the caller.
//
// One query vocabulary for all three, the ledger's: window (today, 7d,
// 30d, 90d, 1y, all) or since/until (a date, or a date with a time for
// an exact instant), plus the page's filters (channels, instances,
// providers, people, models, match). A filter added to the page is read
// through the same parseFilter / Scope, so it reaches these as well.

// UsageReport is the token ledger for the window: totals, trend and the
// breakdown by provider, model, project and person. With q["provider"]
// ("claude/work") it is that provider's slice and the sessions it served.
func (h *Handler) UsageReport(ctx context.Context, q url.Values) (any, error) {
	q = h.resolvePeople(ctx, q)
	win, sc := h.ledgerQuery(q)
	if key := strings.TrimSpace(q.Get("provider")); key != "" {
		return h.ledger().Provider(key, win, sc, q.Get("refresh") == "1")
	}
	return h.ledger().Report(win, sc, q.Get("refresh") == "1")
}

// UsageSessions lists conversations by what they spent in the window,
// heaviest first unless q["sort"] says otherwise, at most q["limit"].
func (h *Handler) UsageSessions(ctx context.Context, q url.Values) (any, error) {
	q = h.resolvePeople(ctx, q)
	win, sc := h.ledgerQuery(q)
	limit, _ := strconv.Atoi(q.Get("limit"))
	return h.ledger().Sessions(win, sc, q.Get("refresh") == "1", q.Get("sort"), limit)
}

// UsagePeople is the page's own table data: people, projects, channels and
// providers, with conversation counts and logins. q["section"] keeps one
// of them (people, projects, channels, providers); empty keeps all but the
// per-day series, which is chart data, not an answer.
func (h *Handler) UsagePeople(ctx context.Context, q url.Values) (any, error) {
	q = h.resolvePeople(ctx, q)
	f := parseFilter(pageQuery(q))
	if !f.Text.Empty() {
		f.Text.Alias = h.conditionAlias
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/admin/analytics/users.json", nil)
	if err != nil {
		return nil, err
	}
	out, err := h.buildAnalytics(r, f, nil)
	if err != nil {
		return nil, err
	}
	out.Series = analyticsSeries{}
	for i := range out.Users {
		out.Users[i].Daily = nil
	}
	head := map[string]any{
		"generated_at":          out.GeneratedAt,
		"window":                out.Window,
		"sessions":              out.Sessions,
		"total_users":           out.TotalUsers,
		"active_users_7d":       out.ActiveUsers7,
		"users_in_window":       out.UsersInWindow,
		"unattributed_sessions": out.Unattributed,
	}
	switch strings.TrimSpace(q.Get("section")) {
	case "people", "users":
		head["users"] = out.Users
	case "projects":
		head["projects"] = out.Projects
	case "channels":
		head["channels"] = out.Channels
	case "providers":
		head["providers"] = out.Providers
	default:
		return out, nil
	}
	return head, nil
}

// ledgerQuery reads window and scope the way analyticsLedgerJSON does.
func (h *Handler) ledgerQuery(q url.Values) (usagereport.Window, usagereport.Scope) {
	win := usagereport.WindowFromQuery(q.Get("window"), q.Get("since"), q.Get("until"), time.Now())
	return win, usagereport.Scope{
		Channels:  csv(q.Get("channels")),
		Instances: csv(q.Get("instances")),
		Providers: csv(q.Get("providers")),
		People:    csv(q.Get("people")),
		Models:    csv(q.Get("models")),
		Text:      h.parseConditions(q),
	}
}

// pageQuery translates the ledger's window words into the page's own
// (days / from / to), so one vocabulary drives both builders. The page
// counts whole days, so a time of day is dropped there.
func pageQuery(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = v
	}
	since, until := strings.TrimSpace(q.Get("since")), strings.TrimSpace(q.Get("until"))
	if since != "" || until != "" {
		if len(since) > 10 {
			since = since[:10]
		}
		if len(until) > 10 {
			until = until[:10]
		}
		out.Set("from", since)
		out.Set("to", until)
		return out
	}
	switch strings.TrimSpace(q.Get("window")) {
	case "today":
		out.Set("days", "1")
	case "7d":
		out.Set("days", "7")
	case "30d":
		out.Set("days", "30")
	case "90d":
		out.Set("days", "90")
	case "1y":
		out.Set("days", "365")
	case "all", "":
		if q.Get("days") == "" {
			out.Set("days", "all")
		}
	}
	return out
}

// resolvePeople lets a caller name a person the way people do — "yoga",
// an email — instead of a user id. A term that is already an id is kept;
// any other term becomes every user whose name or email contains it. A
// term matching nobody is kept as typed, so the answer is an honest
// "nothing" rather than a silently widened scope.
func (h *Handler) resolvePeople(ctx context.Context, q url.Values) url.Values {
	terms := csv(q.Get("people"))
	if len(terms) == 0 || h.repo == nil {
		return q
	}
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return q
	}
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, term := range terms {
		matched := false
		needle := strings.ToLower(term)
		for _, u := range users {
			if u.ID == term {
				add(u.ID)
				matched = true
				break
			}
		}
		if !matched {
			for _, u := range users {
				if strings.Contains(strings.ToLower(u.Name), needle) || strings.Contains(strings.ToLower(u.Email), needle) {
					add(u.ID)
					matched = true
				}
			}
		}
		if !matched {
			add(term)
		}
	}
	out := url.Values{}
	for k, v := range q {
		out[k] = v
	}
	out.Set("people", strings.Join(ids, ","))
	return out
}
