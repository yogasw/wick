package teamlink

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/teamlink"
)

func TestParseThread(t *testing.T) {
	for _, tc := range []struct {
		name, link, channel, ts string
		wantCh, wantTS          string
		bad                     bool
	}{
		{name: "permalink", link: "https://acme.slack.com/archives/C0123ABC/p1792044355318329", wantCh: "C0123ABC", wantTS: "1792044355.318329"},
		{name: "thread_ts query wins", link: "https://acme.slack.com/archives/C0123ABC/p1792044999000100?thread_ts=1792044355.318329&cid=C0123ABC", wantCh: "C0123ABC", wantTS: "1792044355.318329"},
		{name: "permalink beats channel", link: " https://acme.slack.com/archives/C9/p1792044355318329 ", channel: "C1", ts: "1700000000.000001", wantCh: "C9", wantTS: "1792044355.318329"},
		{name: "channel and ts", channel: "C1", ts: "1700000000.000001", wantCh: "C1", wantTS: "1700000000.000001"},
		{name: "not slack", link: "https://example.com/archives/C1/p1792044355318329", bad: true},
		{name: "short p", link: "https://acme.slack.com/archives/C1/p17920443553", bad: true},
		{name: "bad ts", channel: "C1", ts: "1792044355", bad: true},
		{name: "bad query ts", link: "https://acme.slack.com/archives/C1/p1792044355318329?thread_ts=x", bad: true},
		{name: "nothing", bad: true},
		{name: "channel only", channel: "C1", bad: true},
	} {
		ch, ts, err := ParseThread(tc.link, tc.channel, tc.ts)
		if tc.bad {
			if err == nil {
				t.Errorf("%s: want error, got %s %s", tc.name, ch, ts)
			}
			continue
		}
		if err != nil || ch != tc.wantCh || ts != tc.wantTS {
			t.Errorf("%s: got %q %q %v, want %q %q", tc.name, ch, ts, err, tc.wantCh, tc.wantTS)
		}
	}
}

// session_lookup speaks as the calling session's agent; a session of no
// Team agent is refused before any lookup.
func TestSessionLookupPassesCaller(t *testing.T) {
	d := deps(teamlink.NewHub(dir{}, turns{}, &notify{}))
	var got []string
	d.Lookup = func(_ context.Context, agent, session, channel, ts string) (any, error) {
		got = []string{agent, session, channel, ts}
		return "ok", nil
	}
	if _, err := call(t, d, Deps.sessionLookup, "plain-session", map[string]string{"channel": "C1", "thread_ts": "1700000000.000001"}); !errors.Is(err, teamlink.ErrNotTeamSession) {
		t.Fatalf("non-Team session: %v", err)
	}
	if got != nil {
		t.Fatalf("lookup ran for a non-Team session")
	}
	if _, err := call(t, d, Deps.sessionLookup, "sess-cap", map[string]string{"permalink": "https://acme.slack.com/archives/C7/p1792044355318329"}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"a-cap", "sess-cap", "C7", "1792044355.318329"}; len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("lookup got %v, want %v", got, want)
	}
	if _, err := call(t, d, Deps.sessionLookup, "sess-cap", map[string]string{"channel": "C7"}); err == nil {
		t.Fatal("missing thread_ts must be refused")
	}
	d.Lookup = nil
	if _, err := call(t, d, Deps.sessionLookup, "sess-cap", map[string]string{"channel": "C7", "thread_ts": "1700000000.000001"}); !errors.Is(err, errUnavailable) {
		t.Fatalf("no Lookup wired: %v", err)
	}
}
