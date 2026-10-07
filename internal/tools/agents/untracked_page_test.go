package agents

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// railFixture is 300 chats with pool-known sessions scattered through the
// list, clocks far fresher (and staler) than their saved ones, queued ones,
// and blocks of chats saved in the same millisecond — every case where
// stopping the walk early, or ranking a tie, could go wrong.
func railFixture() (map[string]session.Session, map[string]view.SessionLifecycleVM) {
	base := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	sessions := map[string]session.Session{}
	lc := map[string]view.SessionLifecycleVM{}
	for i := 0; i < 300; i++ {
		id := fmt.Sprintf("s%03d", i)
		m := session.Meta{ProjectID: "p", LastActive: base.Add(time.Duration(i*37%300) * time.Minute)}
		if i%7 == 0 {
			m.LastActive = base.Add(time.Duration(i/50) * time.Minute) // ties
		}
		if i%10 == 0 {
			m.ProjectID = "other" // filtered out
		}
		if i%29 == 0 {
			m.Status = "queued"
		}
		sessions[id] = session.Session{Meta: m}
		switch i % 17 {
		case 3: // pool saw it long after its last save
			lc[id] = view.SessionLifecycleVM{Lifecycle: "idle", LastActiveMs: base.Add(900 * time.Minute).UnixMilli()}
		case 5:
			lc[id] = view.SessionLifecycleVM{Lifecycle: "working"}
		}
	}
	return sessions, lc
}

// registryOrder is the list the handler walks: saved LastActive, newest
// first. Ties are broken AGAINST the id on purpose, so a walk that trusted
// the registry's order among equal ages would page wrong.
func registryOrder(sessions map[string]session.Session) []string {
	ordered := make([]string, 0, len(sessions))
	for id := range sessions {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := sessions[ordered[i]].Meta.LastActive, sessions[ordered[j]].Meta.LastActive
		if !a.Equal(b) {
			return a.After(b)
		}
		return ordered[i] > ordered[j]
	})
	return ordered
}

// fullRail is what the rail would be if every row were sorted at once.
func fullRail(ordered []string, sessions map[string]session.Session, lc map[string]view.SessionLifecycleVM, keep func(string, session.Session) bool) []string {
	var all []string
	for _, id := range ordered {
		if keep(id, sessions[id]) {
			all = append(all, id)
		}
	}
	return orderSidebarIDs(all, sessions, lc)
}

// afterIn is the slice of full that follows row `last` (from the top when
// last is ""), at most limit long — what one cursor page must equal.
func afterIn(full []string, last string, limit int) []string {
	i := 0
	if last != "" {
		for i < len(full) && full[i] != last {
			i++
		}
		i++
	}
	if i >= len(full) {
		return nil
	}
	end := i + limit
	if end > len(full) {
		end = len(full)
	}
	return full[i:end]
}

// Walking the rail page by page by cursor yields exactly the full sort, in
// order, with nothing skipped or repeated — the early stop is an
// optimisation, never a change in what the rail shows.
func TestPageLooseSessionsCursorMatchesFullSort(t *testing.T) {
	sessions, lc := railFixture()
	ordered := registryOrder(sessions)
	keep := func(_ string, s session.Session) bool { return s.Meta.ProjectID == "p" }
	full := fullRail(ordered, sessions, lc, keep)

	for _, limit := range []int{1, 7, 25, 200, 400} {
		var walked []string
		var after *railKey
		for pages := 0; ; pages++ {
			if pages > len(full)+1 {
				t.Fatalf("limit=%d: cursor never ran out", limit)
			}
			got, next, total := pageLooseSessions(ordered, sessions, lc, keep, after, limit, true)
			if total != len(full) {
				t.Fatalf("total = %d, want %d", total, len(full))
			}
			last := ""
			if len(walked) > 0 {
				last = walked[len(walked)-1]
			}
			if want := afterIn(full, last, limit); !reflect.DeepEqual(got, want) {
				t.Fatalf("limit=%d page %d:\n got %v\nwant %v", limit, pages, got, want)
			}
			walked = append(walked, got...)
			if next == "" {
				break
			}
			k, err := decodeRailCursor(next)
			if err != nil {
				t.Fatalf("next cursor %q: %v", next, err)
			}
			after = &k
		}
		if !reflect.DeepEqual(walked, full) {
			t.Fatalf("limit=%d: walked %d rows, want the %d of the full sort", limit, len(walked), len(full))
		}
	}

	// Rows not asked for: the count still arrives, nothing else is built.
	got, next, total := pageLooseSessions(ordered, sessions, lc, keep, nil, 25, false)
	if got != nil || next != "" || total != len(full) {
		t.Fatalf("wantRows=false: got %v next %q total %d", got, next, total)
	}
}

// The case the offset got wrong: between two page loads, chats the client
// already holds leave the untracked set (attached elsewhere, ticketed
// off-board, deleted) and one is used and jumps to the top. The next page
// must still start right after the last row the client holds — not one row
// further per chat that vanished.
func TestPageLooseSessionsCursorSurvivesChangesBetweenPages(t *testing.T) {
	sessions, lc := railFixture()
	gone := map[string]bool{}
	keep := func(sid string, s session.Session) bool { return s.Meta.ProjectID == "p" && !gone[sid] }

	ordered := registryOrder(sessions)
	full := fullRail(ordered, sessions, lc, keep)
	first, next, _ := pageLooseSessions(ordered, sessions, lc, keep, nil, 25, true)
	if !reflect.DeepEqual(first, full[:25]) || next == "" {
		t.Fatalf("first page = %v (next %q), want %v", first, next, full[:25])
	}
	second, next2, _ := pageLooseSessions(ordered, sessions, lc, keep, mustCursor(t, next), 50, true)
	if !reflect.DeepEqual(second, full[25:75]) || next2 == "" {
		t.Fatalf("second page = %v, want %v", second, full[25:75])
	}
	held := append(append([]string{}, first...), second...)
	lastHeld := held[len(held)-1]

	// Three held chats leave the set, one held chat and one never-seen chat
	// are used right now.
	gone[held[3]], gone[held[30]], gone[held[60]] = true, true, true
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, sid := range []string{held[40], full[200]} {
		s := sessions[sid]
		s.Meta.LastActive = now
		sessions[sid] = s
	}

	ordered = registryOrder(sessions)
	fullNow := fullRail(ordered, sessions, lc, keep)
	third, _, total := pageLooseSessions(ordered, sessions, lc, keep, mustCursor(t, next2), 50, true)
	if total != len(full)-3 {
		t.Fatalf("total = %d, want %d", total, len(full)-3)
	}
	if want := afterIn(fullNow, lastHeld, 50); !reflect.DeepEqual(third, want) {
		t.Fatalf("third page:\n got %v\nwant %v", third, want)
	}
	// Nothing the client never saw is skipped: every row of the current rail
	// below the last held one, up to the page size, is on the third page —
	// the offset rule would have started 3 rows late.
	if third[0] != full[75] {
		t.Fatalf("third page starts at %s, want %s (the row right after the last held one)", third[0], full[75])
	}
}

func mustCursor(t *testing.T, s string) *railKey {
	t.Helper()
	k, err := decodeRailCursor(s)
	if err != nil {
		t.Fatalf("cursor %q: %v", s, err)
	}
	return &k
}

// A cursor is the server's own: it round-trips, and anything else is
// refused rather than paged from a guess.
func TestRailCursorRoundTripAndRejectsGarbage(t *testing.T) {
	for _, k := range []railKey{{running: true, at: 0, id: "a"}, {at: 1790656524980, id: "x|y"}} {
		got, err := decodeRailCursor(k.encode())
		if err != nil || got != k {
			t.Fatalf("round trip %+v: got %+v, %v", k, got, err)
		}
	}
	for _, bad := range []string{"%%%", "", "MXwy", "Mnx4fGlk", "MXwtMXxpZA", "MXx4fGlk", "MXwxfA"} {
		if _, err := decodeRailCursor(bad); err == nil {
			t.Fatalf("cursor %q accepted", bad)
		}
	}
}

// A chat whose own process is idle while a delegated child works is ranked
// as running, so its row must say so too — not the raw pool "idle" that
// would draw it pinned to the top with no badge.
func TestSessionRowLifecycleMatchesOrder(t *testing.T) {
	live := map[string]session.Session{
		"parent": {Meta: session.Meta{ProjectID: "p"}},
		"plain":  {Meta: session.Meta{ProjectID: "p"}},
	}
	lc := map[string]view.SessionLifecycleVM{
		"parent": {Lifecycle: "idle", SubAgent: "working"},
		"plain":  {Lifecycle: "idle"},
	}
	if got := sessionRow("parent", live, lc, nil).Lifecycle; got != "subagent" {
		t.Fatalf("parent lifecycle = %q, want subagent", got)
	}
	if got := sessionRow("plain", live, lc, nil).Lifecycle; got != "idle" {
		t.Fatalf("plain lifecycle = %q, want idle", got)
	}
	if got := sessionRow("gone", live, lc, nil).Lifecycle; got != "" {
		t.Fatalf("unknown lifecycle = %q, want empty", got)
	}
}
