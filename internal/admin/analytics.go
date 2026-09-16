package admin

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
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
	maxWindowDays     = 180
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
	Days      int                         `json:"days"`
	From      string                      `json:"from"`
	Points    []analyticsPoint            `json:"points"`
	ByChannel map[string][]analyticsPoint `json:"by_channel,omitempty"`
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
	Sessions     int                `json:"sessions"` // conversations they started
	Joined       int                `json:"joined"`   // took part in but did not start
	LastActiveAt string             `json:"last_active_at,omitempty"`
	Channels     []string           `json:"channels,omitempty"` // slack, telegram, ui, rest…
	Projects     []analyticsRef     `json:"projects,omitempty"` // id + name, so the UI shows the name
	Agents       []string           `json:"agents,omitempty"`
	Tokens       []analyticsToken   `json:"tokens,omitempty"`
	Daily        []analyticsPoint   `json:"daily,omitempty"` // this person's own curve
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
	ID           string `json:"id"`
	Label        string `json:"label,omitempty"`
	Channel      string `json:"channel"`
	User         string `json:"user,omitempty"`  // display name, "" when unattributed
	Token        string `json:"token,omitempty"` // the PAT label, for machine callers
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
	GeneratedAt  string             `json:"generated_at"`
	Users        []analyticsUser    `json:"users"`
	Channels     []analyticsChannel `json:"channels"`
	Projects     []analyticsProject `json:"projects"`
	Series       analyticsSeries    `json:"series"`
	TotalUsers   int                `json:"total_users"`
	ActiveUsers7 int                `json:"active_users_7d"`
	Sessions     int                `json:"sessions"`
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
}

// Per-project detail caps. Generous enough to answer the question, small
// enough that the payload does not grow with the install.
const (
	maxProjectMembers = 12
	maxProjectRecent  = 12
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
func windowDays(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n == 0 {
		return defaultWindowDays
	}
	if n < minWindowDays {
		return minWindowDays
	}
	if n > maxWindowDays {
		return maxWindowDays
	}
	return n
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
	days := windowDays(r.URL.Query().Get("days"))

	if r.URL.Query().Get("stream") != "1" {
		out, err := h.buildAnalytics(r, days, nil)
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
	out, err := h.buildAnalytics(r, days, func(done, total int) {
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
func (h *Handler) buildAnalytics(r *http.Request, days int, progress func(done, total int)) (analyticsResponse, error) {
	ctx := r.Context()
	users, err := h.repo.ListUsers(ctx)
	if err != nil {
		return analyticsResponse{}, err
	}
	logins := h.repo.LoginStats(ctx)
	firstLogin := h.repo.FirstLoginRecordedAt(ctx)

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -(days - 1)).Truncate(24 * time.Hour)
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
	}
	per := map[string]*acc{}
	get := func(id string) *acc {
		a := per[id]
		if a == nil {
			a = &acc{
				channels: map[string]bool{}, projects: map[string]bool{},
				agents: map[string]bool{}, daily: map[string]int{},
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
	projs := map[string]*projAcc{}
	tokenSessions := map[string]int{}

	// Daily buckets for the chart.
	globalDay := map[string]int{}
	globalDayPeople := map[string]map[string]bool{}
	channelDay := map[string]map[string]int{}

	total, unattributed := 0, 0

	ids, _ := session.List(agentsLayout)
	for i, id := range ids {
		if progress != nil {
			progress(i, len(ids))
		}
		sess, err := session.Load(agentsLayout, id)
		if err != nil {
			continue
		}
		m := sess.Meta
		total++
		channel := string(m.Origin)
		if channel == "" {
			channel = "ui"
		}
		projectID := m.ProjectID
		if projectID == "" {
			projectID = "(none)"
		}
		agentName := m.ActiveAgent

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

		p := projs[projectID]
		if p == nil {
			name := names[projectID]
			if name == "" {
				name = projectID
			}
			p = &projAcc{
				row:     analyticsProject{ID: projectID, Name: name},
				users:   map[string]bool{},
				members: map[string]*analyticsProjectMember{},
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
			for _, uid := range m.People() {
				if uid != "" {
					globalDayPeople[day][uid] = true
				}
			}
		}

		people := m.People()
		if len(people) == 0 {
			unattributed++
			c.Unattributed++
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
			a.channels[channel] = true
			a.projects[projectID] = true
			if agentName != "" {
				a.agents[agentName] = true
			}
			chanUsers[channel][uid] = true
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

		p.recent = append(p.recent, analyticsSessionRef{
			ID: id, Label: m.Label, Channel: channel,
			User: firstOf(people), Token: m.TokenName,
			LastActiveAt: rfc3339(m.LastActive),
		})
	}
	if progress != nil {
		progress(len(ids), len(ids))
	}

	out := analyticsResponse{
		GeneratedAt:         rfc3339(now),
		Sessions:            total,
		Unattributed:        unattributed,
		TotalUsers:          len(users),
		LoginsRecordedSince: rfc3339(firstLogin),
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

	window := dayKeys(from, days)
	names7 := map[string]string{} // uid -> display name, for project members
	for _, u := range users {
		display := u.Name
		if display == "" {
			display = u.Email
		}
		names7[u.ID] = display

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
			row.Daily = pointsFor(window, a.daily, loginHistory[u.ID])
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

	for ch, c := range chans {
		c.Users = len(chanUsers[ch])
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

		sort.Slice(p.recent, func(i, j int) bool { return p.recent[i].LastActiveAt > p.recent[j].LastActiveAt })
		if len(p.recent) > maxProjectRecent {
			p.recent = p.recent[:maxProjectRecent]
		}
		for i := range p.recent {
			if p.recent[i].User != "" {
				if n := names7[p.recent[i].User]; n != "" {
					p.recent[i].User = n
				}
			}
		}
		p.row.Recent = p.recent
		out.Projects = append(out.Projects, p.row)
	}
	sort.Slice(out.Projects, func(i, j int) bool { return out.Projects[i].Sessions > out.Projects[j].Sessions })
	if len(out.Projects) > maxProjects {
		out.Projects = out.Projects[:maxProjects]
	}

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
