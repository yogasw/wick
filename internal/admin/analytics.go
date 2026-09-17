package admin

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/admin/view"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/login"
)

// analytics.go answers the questions an admin actually asks about people:
// who is still using this, who stopped, through which door, and — when the
// caller is a machine — with whose credential. Everything here is derived
// from what wick already keeps: the auth sessions table for logins, the
// session files for work, the token table for the machines. Nothing extra
// is recorded to build this page.

// defaultWindowDays is how far back the growth chart looks by default.
// A month is long enough to show a trend and short enough that the page
// stays one screen.
const (
	defaultWindowDays = 30
	minWindowDays     = 7
	// Two years of daily points is ~730 small objects — a payload worth
	// sending. Past that the axis is asking for a different chart, not a
	// longer one.
	maxWindowDays = 730
	// allWindowCap bounds "everything": the window still has to be drawn,
	// and an install with a stray 2019 session should not produce a
	// five-year axis nobody can read.
	allWindowCap = 1095
)

// analyticsPoint is one day of the curve. Sessions and People are derived
// from when a conversation was STARTED, which is the only history the
// session files can honestly reconstruct — a session records when it was
// created and when it was last active, not every day in between.
type analyticsPoint struct {
	Date     string `json:"date"` // YYYY-MM-DD, UTC
	Sessions int    `json:"sessions"`
	People   int    `json:"people,omitempty"` // distinct people who started one
	Logins   int    `json:"logins,omitempty"`
}

// analyticsSeries is the whole chart: the global curve, plus one curve per
// channel so "the rise came from Slack" is readable rather than inferred.
type analyticsSeries struct {
	Days       int                         `json:"days"`
	From       string                      `json:"from"`
	Points     []analyticsPoint            `json:"points"`
	ByChannel  map[string][]analyticsPoint `json:"by_channel,omitempty"`
	ByProvider map[string][]analyticsPoint `json:"by_provider,omitempty"`
}

// analyticsToken is one credential, as the page may show it: never the
// token, only the record of it. A revoked one stays listed because it
// still explains traffic that already happened.
type analyticsToken struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Masked     string `json:"masked"`
	CreatedAt  string `json:"created_at,omitempty"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	Revoked    bool   `json:"revoked,omitempty"`
	Sessions   int    `json:"sessions"` // conversations created with it
}

// analyticsUser is one row of the people table.
type analyticsUser struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
	Approved bool   `json:"approved"`
	Avatar   string `json:"avatar,omitempty"`

	// From the auth sessions table: a login is a row there.
	LastLoginAt string `json:"last_login_at,omitempty"`
	Logins      int    `json:"logins"`
	// A login row that has not expired means a browser somewhere is still
	// signed in as this person.
	SignedIn bool `json:"signed_in"`

	// From the session files: what they actually did.
	Sessions     int            `json:"sessions"` // conversations they started
	Joined       int            `json:"joined"`   // took part in but did not start
	LastActiveAt string         `json:"last_active_at,omitempty"`
	Channels     []string       `json:"channels,omitempty"` // slack, telegram, ui, rest…
	Projects     []analyticsRef `json:"projects,omitempty"` // id + name, so the UI shows the name
	Agents       []string       `json:"agents,omitempty"`
	// Providers and Models are this person's own usage: which account ran
	// their work, and with which model. Same source as the Providers tab
	// (each session's agents.json), aggregated per person so "who leans on
	// what" is answerable without opening every session.
	Providers []analyticsKeyCount `json:"providers,omitempty"`
	Models    []analyticsKeyCount `json:"models,omitempty"`
	Tokens    []analyticsToken    `json:"tokens,omitempty"`
	Daily     []analyticsPoint    `json:"daily,omitempty"` // this person's own curve
	// Recent is the drill-down: their newest conversations, the same shape
	// the project list uses. Capped hard — the panel answers "what were they
	// just working on", not "everything they have ever done".
	Recent []analyticsSessionRef `json:"recent,omitempty"`
}

// analyticsRef is an id with the name a human recognises it by. Projects
// are addressed by UUID everywhere in wick, and a column of UUIDs is a
// column nobody can read.
type analyticsRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// analyticsChannel is one door into wick, and how much traffic came through.
type analyticsChannel struct {
	Channel      string `json:"channel"`
	Sessions     int    `json:"sessions"`
	Users        int    `json:"users"`
	Unattributed int    `json:"unattributed,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
	// Instances splits the channel by the configured bot behind it. One
	// workspace can have several, each connected by a different person.
	Instances []analyticsChannelInstance `json:"instances,omitempty"`
}

// analyticsChannelInstance is ONE configured bot on a channel — a Slack
// app somebody connected, a Telegram bot somebody registered.
//
// "slack: 372 conversations" is not an answer when several bots share the
// workspace: it does not say which one is busy, and it does not say whose
// connection it is. The owner is the person who set that bot up, and the
// instance key is what a filter can be built on.
type analyticsChannelInstance struct {
	// Key identifies the instance as the filter uses it: "<channel>:<owner
	// id>", or "<channel>:default" for a channel with no per-owner
	// instance (ui, rest, schedule).
	Key          string `json:"key"`
	Channel      string `json:"channel"`
	OwnerID      string `json:"owner_id,omitempty"`
	OwnerName    string `json:"owner_name,omitempty"`
	OwnerEmail   string `json:"owner_email,omitempty"`
	Sessions     int    `json:"sessions"`
	Users        int    `json:"users"`
	Unattributed int    `json:"unattributed,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
}

// looksLikeUUID is a shape check, not a validation: it only has to be
// sure the prefix is an id rather than the start of a thread timestamp.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

// channelInstanceOf works out which configured bot a session came through.
//
// Two sources, in order of trust: the persisted thread binding, which the
// channel itself wrote, and the session id, which encodes the instance
// because that is how channel sessions are named
// ("slack-<owner uuid>-<thread ts>"). Neither exists for a session typed
// in the dashboard, and "default" is the honest answer there rather than
// a guess.
func channelInstanceOf(id, channel string, m session.Meta) (key, ownerID string) {
	raw := ""
	if m.ChannelRef != nil {
		raw = strings.TrimSpace(m.ChannelRef.Instance)
	}
	if raw == "" {
		raw = id
	}
	// "slack-<uuid>-<thread ts>" or "slack-<uuid>-": the uuid is fixed
	// width, so the tail needs no parsing.
	if rest, ok := strings.CutPrefix(raw, channel+"-"); ok && len(rest) >= 36 && looksLikeUUID(rest[:36]) {
		ownerID = rest[:36]
	}
	if ownerID == "" {
		return channel + ":default", ""
	}
	return channel + ":" + ownerID, ownerID
}

// analyticsFilter is what the page asked for: which slice of time, and
// which doors. Every number in the response is computed under it — a
// filter that only moved the chart while the totals stayed put would be
// reporting two different things side by side.
type analyticsFilter struct {
	From time.Time // inclusive, UTC midnight
	To   time.Time // inclusive, end of that day
	Days int
	All  bool            // window runs back to the oldest conversation
	Chan map[string]bool // empty = every channel
	// Inst narrows further, to specific bots: "slack:<owner id>". Empty =
	// every instance of whichever channels passed Chan.
	Inst map[string]bool
}

func (f analyticsFilter) keepsChannel(ch string) bool {
	if len(f.Chan) == 0 {
		return true
	}
	return f.Chan[ch]
}

func (f analyticsFilter) keepsInstance(key string) bool {
	if len(f.Inst) == 0 {
		return true
	}
	return f.Inst[key]
}

func (f analyticsFilter) keepsTime(t time.Time) bool {
	if f.All {
		return true
	}
	if t.IsZero() {
		return false
	}
	return !t.Before(f.From) && !t.After(f.To)
}

// analyticsWindow is the filter echoed back, so the page can label its
// numbers with the range they were computed over instead of assuming.
type analyticsWindow struct {
	From      string   `json:"from"`
	To        string   `json:"to"`
	Days      int      `json:"days"`
	All       bool     `json:"all,omitempty"`
	Channels  []string `json:"channels,omitempty"`
	Instances []string `json:"instances,omitempty"`
}

// analyticsProvider is one provider instance — an account, in practice —
// and the models it was actually run with.
//
// Read from each session's agents.json, which records the provider key
// ("claude/claude_waba" = type/instance) and any pinned model. That file
// is per session and permanent, unlike the spawn log, which keeps only
// the newest 50 and so cannot answer "which account do we lean on".
type analyticsProvider struct {
	Key          string              `json:"key"`  // claude/claude_waba
	Type         string              `json:"type"` // claude
	Instance     string              `json:"instance,omitempty"`
	Sessions     int                 `json:"sessions"`
	Users        int                 `json:"users"`
	LastActiveAt string              `json:"last_active_at,omitempty"`
	Models       []analyticsKeyCount `json:"models,omitempty"`
}

// analyticsKeyCount is a small "this much, of that kind" pair.
type analyticsKeyCount struct {
	Key      string `json:"key"`
	Sessions int    `json:"sessions"`
}

// analyticsProjectMember is one person's footprint inside one project.
type analyticsProjectMember struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email,omitempty"`
	Sessions     int    `json:"sessions"`
	LastActiveAt string `json:"last_active_at,omitempty"`
}

// analyticsSessionRef is one conversation, as the project drill-down lists
// it. Enough to recognise and open it, not a second copy of the session.
type analyticsSessionRef struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Channel string `json:"channel"`
	User    string `json:"user,omitempty"`  // display name, "" when unattributed
	Token   string `json:"token,omitempty"` // the PAT label, for machine callers
	// Providers is which account ran it. A conversation can switch provider
	// mid-life, so this is a list: naming only the first would misreport the
	// ones that moved.
	Providers []string `json:"providers,omitempty"`
	// The project it lands in. Redundant inside a project's own list, and
	// the whole point inside a person's.
	ProjectID    string `json:"project_id,omitempty"`
	Project      string `json:"project,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`
}

// analyticsProject mirrors the channel view for projects, which is where
// the work lands — with the detail the summary row cannot hold.
type analyticsProject struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Sessions     int    `json:"sessions"`
	Users        int    `json:"users"`
	Unattributed int    `json:"unattributed,omitempty"`
	LastActiveAt string `json:"last_active_at,omitempty"`

	// Filled for the drill-down. Capped: a project with 800 conversations
	// should not put 800 rows in every page load, and the top of each list
	// is what the question "who works here" actually wants.
	Members  []analyticsProjectMember `json:"members,omitempty"`
	Channels []analyticsKeyCount      `json:"channels,omitempty"`
	Recent   []analyticsSessionRef    `json:"recent,omitempty"`
}

type analyticsResponse struct {
	GeneratedAt string              `json:"generated_at"`
	Window      analyticsWindow     `json:"window"`
	Users       []analyticsUser     `json:"users"`
	Channels    []analyticsChannel  `json:"channels"`
	Projects    []analyticsProject  `json:"projects"`
	Providers   []analyticsProvider `json:"providers,omitempty"`
	Series      analyticsSeries     `json:"series"`
	// TotalUsers counts accounts, which exist regardless of the window.
	TotalUsers   int `json:"total_users"`
	ActiveUsers7 int `json:"active_users_7d"`
	// Sessions is conversations INSIDE the window. SessionsAllTime is every
	// conversation on disk, kept so the page can show what it is a slice of
	// rather than looking like the install shrank when a range is picked.
	Sessions        int `json:"sessions"`
	SessionsAllTime int `json:"sessions_all_time"`
	// UsersInWindow is how many people started a conversation in it.
	UsersInWindow int `json:"users_in_window"`
	// LoginsRecordedSince is the oldest sign-in on record, empty when none
	// is. Sign-ins were not written down at all before this shipped —
	// wick's sessions are stateless — so without this the page would report
	// every account as "never signed in", which is a different and much
	// stronger claim than "no record".
	LoginsRecordedSince string `json:"logins_recorded_since,omitempty"`
	// Unattributed sessions predate user stamping (or came from a channel
	// with no wick account behind it). Counted rather than hidden: a table
	// whose numbers do not add up to the total invites the wrong conclusion.
	Unattributed int `json:"unattributed_sessions"`
	// KnownChannels is every channel ever seen, regardless of the filter —
	// otherwise filtering to one channel would remove the means of
	// filtering back out of it.
	KnownChannels []string `json:"known_channels,omitempty"`
}

// Per-project detail caps. Generous enough to answer the question, small
// enough that the payload does not grow with the install.
const (
	maxProjectMembers = 12
	maxProjectRecent  = 12
	maxUserRecent     = 5
	maxProjects       = 40
)

// agentsLayout is the session store the analytics read. Set by the server
// once, from the same resolved base dir the agents tool uses — admin has no
// business resolving that path a second time and getting it subtly wrong.
var agentsLayout agentconfig.Layout

// SetAgentsLayout wires the session store for the analytics page.
func SetAgentsLayout(l agentconfig.Layout) { agentsLayout = l }

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// later keeps the newest of two stamps, treating zero as "never".
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// windowDays reads the ?days= override, clamped. Out-of-range values are
// clamped rather than rejected: a chart is not worth a 400.
//
// "all" is its own answer: the window is not known until the data has been
// read, because it runs back to the oldest conversation on disk.
func windowDays(raw string) (days int, all bool) {
	if strings.EqualFold(strings.TrimSpace(raw), "all") {
		return defaultWindowDays, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n == 0 {
		return defaultWindowDays, false
	}
	if n < minWindowDays {
		return minWindowDays, false
	}
	if n > maxWindowDays {
		return maxWindowDays, false
	}
	return n, false
}

// parseFilter reads the window and the channel filter off the query.
//
// Bad input is clamped rather than refused: this is a dashboard, and a
// mistyped date is better answered with the default range than with a
// 400 the page has to render as an error.
func parseFilter(q url.Values) analyticsFilter {
	now := time.Now().UTC()
	f := analyticsFilter{Chan: map[string]bool{}, Inst: map[string]bool{}}

	for _, raw := range strings.Split(q.Get("channels"), ",") {
		ch := strings.TrimSpace(raw)
		if ch != "" && !strings.EqualFold(ch, "all") {
			f.Chan[ch] = true
		}
	}
	f.Inst = map[string]bool{}
	for _, raw := range strings.Split(q.Get("instances"), ",") {
		key := strings.TrimSpace(raw)
		if key != "" && !strings.EqualFold(key, "all") {
			f.Inst[key] = true
		}
	}

	// A custom range wins over the day count: it is the more specific ask.
	fromStr, toStr := strings.TrimSpace(q.Get("from")), strings.TrimSpace(q.Get("to"))
	if fromStr != "" || toStr != "" {
		from, errFrom := time.Parse("2006-01-02", fromStr)
		to, errTo := time.Parse("2006-01-02", toStr)
		if errFrom != nil {
			from = now.AddDate(0, 0, -(defaultWindowDays - 1))
		}
		if errTo != nil {
			to = now
		}
		if to.Before(from) {
			from, to = to, from // a backwards range is a slip, not a request for nothing
		}
		f.From = from.UTC().Truncate(24 * time.Hour)
		f.To = endOfDay(to.UTC())
		f.Days = int(f.To.Sub(f.From).Hours()/24) + 1
		if f.Days > allWindowCap {
			f.Days = allWindowCap
			f.From = f.To.AddDate(0, 0, -(f.Days - 1)).Truncate(24 * time.Hour)
		}
		return f
	}

	days, all := windowDays(q.Get("days"))
	f.Days, f.All = days, all
	f.To = endOfDay(now)
	f.From = now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
	if all {
		f.From = time.Time{} // resolved after the read, from the oldest conversation
	}
	return f
}

func endOfDay(t time.Time) time.Time {
	return t.Truncate(24 * time.Hour).Add(24*time.Hour - time.Nanosecond)
}

// dayKeys returns the window's days, oldest first. The chart is built from
// this rather than from the data so quiet days appear as zeros instead of
// vanishing — a gap in a line chart reads as "no data", which is a
// different claim from "nobody came".
func dayKeys(from time.Time, days int) []string {
	out := make([]string, 0, days)
	for i := 0; i < days; i++ {
		out = append(out, from.AddDate(0, 0, i).Format("2006-01-02"))
	}
	return out
}

// analyticsUsersJSON is the whole page's data in one request. One call, not
// one per user: the page shows a table, and a table that fires N requests to
// fill N rows is how a 40-person install becomes unusable.
//
// With ?stream=1 the same data arrives as NDJSON, preceded by progress
// lines. Reading several hundred session files takes seconds, and a page
// that shows nothing for those seconds is indistinguishable from a page
// that has hung.
func (h *Handler) analyticsUsersJSON(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r.URL.Query())

	if r.URL.Query().Get("stream") != "1" {
		out, err := h.buildAnalytics(r, f, nil)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Streaming path. Status and headers go out before the slow work, so
	// the browser can start rendering the progress bar immediately.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx would otherwise hold the lines
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	emit := func(v any) {
		_ = enc.Encode(v)
		if flusher != nil {
			flusher.Flush()
		}
	}

	last := time.Time{}
	out, err := h.buildAnalytics(r, f, func(done, total int) {
		// Throttled: a flush per session file would spend more time
		// writing progress than reading data.
		if done < total && time.Since(last) < 120*time.Millisecond {
			return
		}
		last = time.Now()
		emit(map[string]any{"type": "progress", "done": done, "total": total})
	})
	if err != nil {
		emit(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	emit(map[string]any{"type": "result", "data": out})
}

// buildAnalytics does the reading. progress, when non-nil, is called as
// session files are consumed so a caller can report how far along it is.
func (h *Handler) buildAnalytics(r *http.Request, f analyticsFilter, progress func(done, total int)) (analyticsResponse, error) {
	days, all := f.Days, f.All
	ctx := r.Context()
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return analyticsResponse{}, err
	}
	logins := h.repo.LoginStats(ctx)
	firstLogin := h.repo.FirstLoginRecordedAt(ctx)

	now := time.Now().UTC()
	from := f.From
	loginHistory := h.repo.LoginHistory(ctx, from)
	tokens := h.repo.AccessTokens(ctx)

	// Project names, read once. A project folder can disappear while
	// sessions referencing it remain, so a missing name falls back to the
	// id rather than dropping the row.
	names := map[string]string{}
	if ids, err := project.List(agentsLayout); err == nil {
		for _, id := range ids {
			if p, err := project.Load(agentsLayout, id); err == nil && p.Meta.Name != "" {
				names[id] = p.Meta.Name
			}
		}
	}

	// Per-user accumulators, keyed by wick user id.
	type acc struct {
		sessions, joined int
		lastActive       time.Time
		channels         map[string]bool
		projects         map[string]bool
		agents           map[string]bool
		daily            map[string]int
		providers        map[string]int
		models           map[string]int
		recent           []analyticsSessionRef
	}
	per := map[string]*acc{}
	get := func(id string) *acc {
		a := per[id]
		if a == nil {
			a = &acc{
				channels: map[string]bool{}, projects: map[string]bool{},
				agents: map[string]bool{}, daily: map[string]int{},
				providers: map[string]int{}, models: map[string]int{},
			}
			per[id] = a
		}
		return a
	}

	type projAcc struct {
		row      analyticsProject
		users    map[string]bool
		members  map[string]*analyticsProjectMember
		channels map[string]int
		recent   []analyticsSessionRef
	}
	chans := map[string]*analyticsChannel{}
	chanUsers := map[string]map[string]bool{}
	insts := map[string]*analyticsChannelInstance{}
	instUsers := map[string]map[string]bool{}
	projs := map[string]*projAcc{}
	tokenSessions := map[string]int{}

	// Daily buckets for the chart.
	globalDay := map[string]int{}
	globalDayPeople := map[string]map[string]bool{}
	channelDay := map[string]map[string]int{}
	providerDay := map[string]map[string]int{}

	type provAcc struct {
		row    analyticsProvider
		users  map[string]bool
		models map[string]int
	}
	provs := map[string]*provAcc{}

	total, unattributed, allTime := 0, 0, 0
	// Every channel ever seen, filtered or not: filtering to one channel
	// must not remove the means of filtering back out of it.
	allChannels := map[string]bool{}
	var oldest time.Time

	ids, _ := session.List(agentsLayout)
	alive := make(map[string]bool, len(ids))
	for i, id := range ids {
		if progress != nil {
			progress(i, len(ids))
		}
		alive[id] = true
		// Cached by the file's own mtime+size, so a second render costs a
		// stat per session instead of a read and a parse. The first load
		// still pays, which is what the progress bar is for.
		m, ok := sessionCache.get(agentsLayout, id)
		if !ok {
			continue
		}
		allTime++
		channel := m.Channel
		allChannels[channel] = true

		instKey, instOwner := m.InstanceKey, m.InstanceOwn

		// The filter applies HERE, before anything is counted — so every
		// number below describes the same slice the chart does. A page
		// where the chart moved but the totals did not would be reporting
		// two different things side by side.
		if !f.keepsChannel(channel) || !f.keepsInstance(instKey) {
			continue
		}
		if !m.CreatedAt.IsZero() && (oldest.IsZero() || m.CreatedAt.Before(oldest)) {
			oldest = m.CreatedAt.UTC()
		}
		if !f.keepsTime(m.CreatedAt) {
			continue
		}
		total++
		projectID := m.ProjectID
		agentName := m.Agent

		c := chans[channel]
		if c == nil {
			c = &analyticsChannel{Channel: channel}
			chans[channel] = c
			chanUsers[channel] = map[string]bool{}
		}
		c.Sessions++
		if t := m.LastActive; t.After(parseStamp(c.LastActiveAt)) {
			c.LastActiveAt = rfc3339(t)
		}

		in := insts[instKey]
		if in == nil {
			in = &analyticsChannelInstance{Key: instKey, Channel: channel, OwnerID: instOwner}
			insts[instKey] = in
			instUsers[instKey] = map[string]bool{}
		}
		in.Sessions++
		if t := m.LastActive; t.After(parseStamp(in.LastActiveAt)) {
			in.LastActiveAt = rfc3339(t)
		}

		p := projs[projectID]
		if p == nil {
			name := names[projectID]
			if name == "" {
				name = projectID
			}
			p = &projAcc{
				row:      analyticsProject{ID: projectID, Name: name},
				users:    map[string]bool{},
				members:  map[string]*analyticsProjectMember{},
				channels: map[string]int{},
			}
			projs[projectID] = p
		}
		p.row.Sessions++
		p.channels[channel]++
		if t := m.LastActive; t.After(parseStamp(p.row.LastActiveAt)) {
			p.row.LastActiveAt = rfc3339(t)
		}

		if m.TokenID != "" {
			tokenSessions[m.TokenID]++
		}

		// Which provider instance — which ACCOUNT — ran this conversation,
		// and with which model. A session can switch provider mid-life, so
		// every entry in its agents.json counts once.
		var sessProviders, sessModels []string
		for _, use := range m.Providers {
			key := use.Key
			pv := provs[key]
			if pv == nil {
				typ, instance := key, ""
				if slash := strings.IndexByte(key, '/'); slash >= 0 {
					typ, instance = key[:slash], key[slash+1:]
				}
				pv = &provAcc{
					row:    analyticsProvider{Key: key, Type: typ, Instance: instance},
					users:  map[string]bool{},
					models: map[string]int{},
				}
				provs[key] = pv
			}
			pv.row.Sessions++
			if t := m.LastActive; t.After(parseStamp(pv.row.LastActiveAt)) {
				pv.row.LastActiveAt = rfc3339(t)
			}
			model := use.Model
			pv.models[model]++
			sessProviders = append(sessProviders, key)
			sessModels = append(sessModels, model)
			for _, uid := range m.People {
				if uid != "" {
					pv.users[uid] = true
				}
			}
			if !m.CreatedAt.Before(from) {
				day := m.CreatedAt.UTC().Format("2006-01-02")
				if providerDay[key] == nil {
					providerDay[key] = map[string]int{}
				}
				providerDay[key][day]++
			}
		}

		// The chart counts conversations by the day they STARTED.
		if !m.CreatedAt.Before(from) {
			day := m.CreatedAt.UTC().Format("2006-01-02")
			globalDay[day]++
			if globalDayPeople[day] == nil {
				globalDayPeople[day] = map[string]bool{}
			}
			if channelDay[channel] == nil {
				channelDay[channel] = map[string]int{}
			}
			channelDay[channel][day]++
			for _, uid := range m.People {
				if uid != "" {
					globalDayPeople[day][uid] = true
				}
			}
		}

		// One row, shared by the drill-downs: the project's list and each
		// person's. Built once so the two can never disagree about a session.
		ref := analyticsSessionRef{
			ID: id, Label: m.Label, Channel: channel,
			User: firstOf(m.People), Token: m.TokenName,
			Providers: sessProviders,
			ProjectID: projectID, Project: p.row.Name,
			LastActiveAt: rfc3339(m.LastActive),
		}
		p.recent = append(p.recent, ref)

		people := m.People
		if len(people) == 0 {
			unattributed++
			c.Unattributed++
			in.Unattributed++
			p.row.Unattributed++
		}
		for i, uid := range people {
			if uid == "" {
				continue
			}
			a := get(uid)
			// People() starts with the creator; everyone after it joined.
			if i == 0 {
				a.sessions++
				if !m.CreatedAt.Before(from) {
					a.daily[m.CreatedAt.UTC().Format("2006-01-02")]++
				}
			} else {
				a.joined++
			}
			a.lastActive = later(a.lastActive, m.LastActive)
			for _, key := range sessProviders {
				a.providers[key]++
			}
			for _, model := range sessModels {
				a.models[model]++
			}
			a.channels[channel] = true
			a.projects[projectID] = true
			a.recent = append(a.recent, ref)
			if agentName != "" {
				a.agents[agentName] = true
			}
			chanUsers[channel][uid] = true
			instUsers[instKey][uid] = true
			p.users[uid] = true

			mem := p.members[uid]
			if mem == nil {
				mem = &analyticsProjectMember{ID: uid}
				p.members[uid] = mem
			}
			mem.Sessions++
			if t := m.LastActive; t.After(parseStamp(mem.LastActiveAt)) {
				mem.LastActiveAt = rfc3339(t)
			}
		}
	}
	if progress != nil {
		progress(len(ids), len(ids))
	}
	// A session that no longer exists stops being remembered, so the cache
	// stays bounded by what is on disk rather than by everything this
	// process has ever read.
	sessionCache.keep(alive)

	out := analyticsResponse{
		GeneratedAt:         rfc3339(now),
		Sessions:            total,
		SessionsAllTime:     allTime,
		Unattributed:        unattributed,
		TotalUsers:          len(users),
		UsersInWindow:       len(per),
		LoginsRecordedSince: rfc3339(firstLogin),
		KnownChannels:       sortedKeys(allChannels),
	}

	// Token rows, grouped by owner.
	byOwner := map[string][]analyticsToken{}
	for _, t := range tokens {
		row := analyticsToken{
			ID: t.ID, Name: t.Name, Masked: t.Masked(),
			CreatedAt: rfc3339(t.CreatedAt),
			Revoked:   t.RevokedAt != nil,
			Sessions:  tokenSessions[t.ID],
		}
		if t.LastUsedAt != nil {
			row.LastUsedAt = rfc3339(*t.LastUsedAt)
		}
		byOwner[t.UserID] = append(byOwner[t.UserID], row)
	}

	if all {
		// The axis now runs from the oldest conversation to today. With no
		// sessions at all there is nothing to span, so it falls back to the
		// default window rather than drawing a chart from year one.
		if oldest.IsZero() {
			from = now.AddDate(0, 0, -(defaultWindowDays - 1)).Truncate(24 * time.Hour)
			days = defaultWindowDays
		} else {
			from = oldest.Truncate(24 * time.Hour)
			days = int(now.Sub(from).Hours()/24) + 1
			if days > allWindowCap {
				days = allWindowCap
				from = now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
			}
			if days < minWindowDays {
				days = minWindowDays
				from = now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
			}
		}
	}

	window := dayKeys(from, days)
	names7 := map[string]string{} // uid -> display name, for the drill-downs
	for _, u := range users {
		display := u.Name
		if display == "" {
			display = u.Email
		}
		names7[u.ID] = display
	}
	for _, u := range users {
		row := analyticsUser{
			ID: u.ID, Name: u.Name, Email: u.Email, Approved: u.Approved,
			Role: string(u.Role), Avatar: u.Avatar,
		}
		if u.IsOwner {
			row.Role = "owner"
		}
		if ls, ok := logins[u.ID]; ok {
			row.LastLoginAt = rfc3339(ls.Last)
			row.Logins = ls.Count
			row.SignedIn = ls.Live > 0
		}
		row.Tokens = byOwner[u.ID]
		sort.Slice(row.Tokens, func(i, j int) bool { return row.Tokens[i].LastUsedAt > row.Tokens[j].LastUsedAt })

		if a := per[u.ID]; a != nil {
			row.Sessions, row.Joined = a.sessions, a.joined
			row.LastActiveAt = rfc3339(a.lastActive)
			row.Channels = sortedKeys(a.channels)
			row.Agents = sortedKeys(a.agents)
			for _, id := range sortedKeys(a.projects) {
				name := names[id]
				if name == "" {
					name = id
				}
				row.Projects = append(row.Projects, analyticsRef{ID: id, Name: name})
			}
			if now.Sub(a.lastActive) <= 7*24*time.Hour && !a.lastActive.IsZero() {
				out.ActiveUsers7++
			}
			row.Providers = rankedCounts(a.providers)
			row.Models = rankedCounts(a.models)
			row.Daily = pointsFor(window, a.daily, loginHistory[u.ID])
			row.Recent = newestFirst(a.recent, maxUserRecent, names7)
		} else if lh := loginHistory[u.ID]; len(lh) > 0 {
			// Signed in but never worked: still a curve worth drawing.
			row.Daily = pointsFor(window, nil, lh)
		}
		out.Users = append(out.Users, row)
	}
	// Most recently active first — the question is "who is still here", and
	// an alphabetical list answers a question nobody asked.
	sort.SliceStable(out.Users, func(i, j int) bool {
		a, b := out.Users[i], out.Users[j]
		ka := maxStamp(a.LastActiveAt, a.LastLoginAt)
		kb := maxStamp(b.LastActiveAt, b.LastLoginAt)
		if ka != kb {
			return ka > kb
		}
		return a.Name < b.Name
	})

	// Owners, resolved from the accounts table: an instance key carries a
	// uuid, and a uuid is not an answer to "whose bot is this".
	owners := map[string]struct{ name, email string }{}
	for _, u := range users {
		display := u.Name
		if display == "" {
			display = u.Email
		}
		owners[u.ID] = struct{ name, email string }{display, u.Email}
	}
	byChannel := map[string][]analyticsChannelInstance{}
	for _, in := range insts {
		in.Users = len(instUsers[in.Key])
		if o, ok := owners[in.OwnerID]; ok {
			in.OwnerName, in.OwnerEmail = o.name, o.email
		}
		byChannel[in.Channel] = append(byChannel[in.Channel], *in)
	}
	for ch, list := range byChannel {
		sort.Slice(list, func(i, j int) bool { return list[i].Sessions > list[j].Sessions })
		byChannel[ch] = list
	}

	for ch, c := range chans {
		c.Users = len(chanUsers[ch])
		c.Instances = byChannel[ch]
		out.Channels = append(out.Channels, *c)
	}
	sort.Slice(out.Channels, func(i, j int) bool { return out.Channels[i].Sessions > out.Channels[j].Sessions })

	for _, p := range projs {
		p.row.Users = len(p.users)
		for uid, mem := range p.members {
			mem.Name = names7[uid]
			if mem.Name == "" {
				mem.Name = uid
			}
			p.row.Members = append(p.row.Members, *mem)
		}
		sort.Slice(p.row.Members, func(i, j int) bool {
			if p.row.Members[i].Sessions != p.row.Members[j].Sessions {
				return p.row.Members[i].Sessions > p.row.Members[j].Sessions
			}
			return p.row.Members[i].Name < p.row.Members[j].Name
		})
		if len(p.row.Members) > maxProjectMembers {
			p.row.Members = p.row.Members[:maxProjectMembers]
		}
		for k, n := range p.channels {
			p.row.Channels = append(p.row.Channels, analyticsKeyCount{Key: k, Sessions: n})
		}
		sort.Slice(p.row.Channels, func(i, j int) bool { return p.row.Channels[i].Sessions > p.row.Channels[j].Sessions })

		p.row.Recent = newestFirst(p.recent, maxProjectRecent, names7)
		out.Projects = append(out.Projects, p.row)
	}
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Sessions > out.Projects[j].Sessions })
	if len(out.Projects) > maxProjects {
		out.Projects = out.Projects[:maxProjects]
	}

	for _, pv := range provs {
		pv.row.Users = len(pv.users)
		for model, n := range pv.models {
			pv.row.Models = append(pv.row.Models, analyticsKeyCount{Key: model, Sessions: n})
		}
		sort.Slice(pv.row.Models, func(i, j int) bool { return pv.row.Models[i].Sessions > pv.row.Models[j].Sessions })
		out.Providers = append(out.Providers, pv.row)
	}
	sort.Slice(out.Providers, func(i, j int) bool {
		if out.Providers[i].Sessions != out.Providers[j].Sessions {
			return out.Providers[i].Sessions > out.Providers[j].Sessions
		}
		return out.Providers[i].Key < out.Providers[j].Key
	})

	// The global curve, plus one per channel.
	globalLogins := map[string]int{}
	for _, byDay := range loginHistory {
		for day, n := range byDay {
			globalLogins[day] += n
		}
	}
	out.Series = analyticsSeries{Days: days, From: from.Format("2006-01-02")}
	for _, day := range window {
		out.Series.Points = append(out.Series.Points, analyticsPoint{
			Date:     day,
			Sessions: globalDay[day],
			People:   len(globalDayPeople[day]),
			Logins:   globalLogins[day],
		})
	}
	if len(channelDay) > 0 {
		out.Series.ByChannel = map[string][]analyticsPoint{}
		for ch, byDay := range channelDay {
			out.Series.ByChannel[ch] = pointsFor(window, byDay, nil)
		}
	}
	if len(providerDay) > 0 {
		out.Series.ByProvider = map[string][]analyticsPoint{}
		for key, byDay := range providerDay {
			out.Series.ByProvider[key] = pointsFor(window, byDay, nil)
		}
	}

	out.Window = analyticsWindow{
		From:      from.Format("2006-01-02"),
		To:        now.Format("2006-01-02"),
		Days:      days,
		All:       all,
		Channels:  sortedKeys(f.Chan),
		Instances: sortedKeys(f.Inst),
	}
	if !f.To.IsZero() && !f.All {
		out.Window.To = f.To.Format("2006-01-02")
	}

	return out, nil
}

// pointsFor expands sparse day counts over the full window, so every
// series has the same x axis and a quiet day is a zero rather than a gap.
func pointsFor(window []string, sessions, logins map[string]int) []analyticsPoint {
	out := make([]analyticsPoint, 0, len(window))
	for _, day := range window {
		out = append(out, analyticsPoint{Date: day, Sessions: sessions[day], Logins: logins[day]})
	}
	return out
}

// rankedCounts turns a tally into a list, busiest first, so the UI can
// show "the one they mostly use" by taking the head.
func rankedCounts(m map[string]int) []analyticsKeyCount {
	if len(m) == 0 {
		return nil
	}
	out := make([]analyticsKeyCount, 0, len(m))
	for k, n := range m {
		out = append(out, analyticsKeyCount{Key: k, Sessions: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sessions != out[j].Sessions {
			return out[i].Sessions > out[j].Sessions
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// newestFirst trims a drill-down list to the newest few and swaps each
// creator's uuid for the name a human recognises. Both the project panel
// and a person's panel run through it, so the two lists read the same way.
func newestFirst(list []analyticsSessionRef, max int, names map[string]string) []analyticsSessionRef {
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LastActiveAt > list[j].LastActiveAt })
	if len(list) > max {
		list = list[:max]
	}
	for i := range list {
		if list[i].User == "" {
			continue
		}
		if n := names[list[i].User]; n != "" {
			list[i].User = n
		}
	}
	return list
}

func firstOf(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// analyticsPage renders the shell the Svelte module mounts into.
func (h *Handler) analyticsPage(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	_ = view.AnalyticsPage(view.AnalyticsSPAVM{
		AssetURL: spaAssetURL(),
		DataURL:  "/admin/analytics/users.json",
	}, user).Render(r.Context(), w)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseStamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func maxStamp(a, b string) string {
	if a > b {
		return a
	}
	return b
}
