package wickmanager

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// UsageSource is the People & usage page, read by the usage_* ops. The
// admin handler implements it; it is wired after the connector is
// registered, so Deps carries a func that returns it rather than the
// value itself.
type UsageSource interface {
	UsageReport(ctx context.Context, q url.Values) (any, error)
	UsageSessions(ctx context.Context, q url.Values) (any, error)
	UsagePeople(ctx context.Context, q url.Values) (any, error)
}

var errUsageUnavailable = errors.New("usage analytics unavailable in this run mode")

// usageQueryKeys are the inputs every usage_* op shares, in the page's
// query vocabulary so a filter reads the same in both places.
var usageQueryKeys = []string{
	"window", "since", "until", "people", "channels", "instances",
	"providers", "models", "provider", "sort", "limit", "section", "refresh",
}

// The usage_* inputs repeat the shared filter fields: the input reflection
// reads one struct's own fields, not an embedded one's.

type usageReportInput struct {
	Window    string `wick:"desc=Range: today, 7d, 30d, 90d, 1y or all (default all). Ignored when since/until is set."`
	Since     string `wick:"desc=Start, YYYY-MM-DD (whole day) or YYYY-MM-DDTHH:MM with optional zone (exact instant, e.g. a provider's rate-limit window start). Server timezone when no zone is given."`
	Until     string `wick:"desc=End, same forms as since. A date alone means the end of that day."`
	People    string `wick:"desc=Comma list of people: user id, or part of a name or email (e.g. yoga). Narrows to conversations they took part in."`
	Channels  string `wick:"desc=Comma list of channels: slack, telegram, ui, rest, workflow…"`
	Instances string `wick:"desc=Comma list of channel instances (slack:<owner id>…)."`
	Providers string `wick:"desc=Comma list of provider keys (claude/work…)."`
	Models    string `wick:"desc=Comma list of models."`
	Provider  string `wick:"desc=One provider key (claude/work): answer that provider's slice and the sessions it served instead of the whole fleet."`
}

type usageSessionsInput struct {
	Window    string `wick:"desc=Range: today, 7d, 30d, 90d, 1y or all (default all). Ignored when since/until is set."`
	Since     string `wick:"desc=Start, YYYY-MM-DD (whole day) or YYYY-MM-DDTHH:MM with optional zone (exact instant, e.g. a provider's rate-limit window start). Server timezone when no zone is given."`
	Until     string `wick:"desc=End, same forms as since. A date alone means the end of that day."`
	People    string `wick:"desc=Comma list of people: user id, or part of a name or email (e.g. yoga). Narrows to conversations they took part in."`
	Channels  string `wick:"desc=Comma list of channels: slack, telegram, ui, rest, workflow…"`
	Instances string `wick:"desc=Comma list of channel instances (slack:<owner id>…)."`
	Providers string `wick:"desc=Comma list of provider keys (claude/work…)."`
	Models    string `wick:"desc=Comma list of models."`
	Sort      string `wick:"desc=tokens (default), cost, turns or recent."`
	Limit     int    `wick:"number;desc=Max rows. 0 = 20."`
}

type usagePeopleInput struct {
	Window    string `wick:"desc=Range: today, 7d, 30d, 90d, 1y or all (default all). Ignored when since/until is set."`
	Since     string `wick:"desc=Start, YYYY-MM-DD (whole day) or YYYY-MM-DDTHH:MM with optional zone (exact instant, e.g. a provider's rate-limit window start). Server timezone when no zone is given."`
	Until     string `wick:"desc=End, same forms as since. A date alone means the end of that day."`
	People    string `wick:"desc=Comma list of people: user id, or part of a name or email (e.g. yoga). Narrows to conversations they took part in."`
	Channels  string `wick:"desc=Comma list of channels: slack, telegram, ui, rest, workflow…"`
	Instances string `wick:"desc=Comma list of channel instances (slack:<owner id>…)."`
	Providers string `wick:"desc=Comma list of provider keys (claude/work…)."`
	Models    string `wick:"desc=Comma list of models."`
	Section   string `wick:"desc=people, projects, channels or providers to return only that table. Empty = everything."`
}

func usageOps(h *handlers) []connector.Operation {
	return []connector.Operation{
		connector.Op("usage_report", "Token Usage Report",
			"Token ledger for a range — the People & usage page's usage card: totals (input, cache read/write, output, cost, cache hit %), turns, sessions, trend, and the breakdown by provider, model, project and person with each row's share. Pass provider to get that provider's slice plus every session it served. Access: ADMIN ONLY. UI: <app_url>/admin/analytics.",
			usageReportInput{}, h.usageReport, wickdocs.Docs{}),
		connector.Op("usage_sessions", "Top Sessions by Usage",
			"Which conversations spent the tokens: one row per session (merged across providers) with label, person, project, channel, agent, providers, turns, token totals, cost, last activity and share of the range total, sorted by tokens unless sort says otherwise. Use since/until with a time to look at one rate-limit window. Access: ADMIN ONLY. UI: <app_url>/admin/analytics.",
			usageSessionsInput{}, h.usageSessions, wickdocs.Docs{}),
		connector.Op("usage_people", "People & Usage",
			"The People & usage page's tables: people (logins, conversations started/joined, channels, projects, providers, models, recent conversations), projects, channels and providers, with conversation counts for the range. Access: ADMIN ONLY. UI: <app_url>/admin/analytics.",
			usagePeopleInput{}, h.usagePeople, wickdocs.Docs{}),
	}
}

func (h *handlers) usageReport(c *connector.Ctx) (any, error) {
	return h.usageCall(c, "usage_report", func(src UsageSource, ctx context.Context, q url.Values) (any, error) {
		return src.UsageReport(ctx, q)
	})
}

func (h *handlers) usageSessions(c *connector.Ctx) (any, error) {
	return h.usageCall(c, "usage_sessions", func(src UsageSource, ctx context.Context, q url.Values) (any, error) {
		if q.Get("limit") == "" || q.Get("limit") == "0" {
			q.Set("limit", "20")
		}
		return src.UsageSessions(ctx, q)
	})
}

func (h *handlers) usagePeople(c *connector.Ctx) (any, error) {
	return h.usageCall(c, "usage_people", func(src UsageSource, ctx context.Context, q url.Values) (any, error) {
		return src.UsagePeople(ctx, q)
	})
}

// usageCall is the shared gate: admin only, because the page names every
// account and what it spent.
func (h *handlers) usageCall(c *connector.Ctx, op string, run func(UsageSource, context.Context, url.Values) (any, error)) (out any, err error) {
	start := time.Now()
	ctx := c.Context()
	q := url.Values{}
	for _, k := range usageQueryKeys {
		if v := strings.TrimSpace(c.Input(k)); v != "" {
			q.Set(k, v)
		}
	}
	user, err := requireAdmin(ctx)
	defer func() { logOp(user, op, map[string]string{"query": q.Encode()}, err, time.Since(start)) }()
	if err != nil {
		return nil, err
	}
	var src UsageSource
	if h.deps.Usage != nil {
		src = h.deps.Usage()
	}
	if src == nil {
		err = errUsageUnavailable
		return nil, err
	}
	return run(src, ctx, q)
}
