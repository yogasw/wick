package provider

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
)

// failoverFixture: home claude/a fails over along codex/b → claude/c →
// codex/d. codex/b has a chain of its own pointing back at claude/a, which
// must never be followed.
func failoverFixture() []Instance {
	return []Instance{
		{Type: TypeClaude, Name: "a", Failover: &FailoverConfig{
			Enabled:    true,
			Triggers:   FailoverTriggers,
			Chain:      []FailoverTarget{{Instance: "codex/b"}, {Instance: "claude/c"}, {Instance: "codex/d"}},
			ReturnHome: true,
		}},
		{Type: TypeCodex, Name: "b", Models: []ModelEntry{{ID: "gpt-5"}}, Failover: &FailoverConfig{
			Enabled: true, Triggers: FailoverTriggers, Chain: []FailoverTarget{{Instance: "claude/a"}},
		}},
		{Type: TypeClaude, Name: "c"},
		{Type: TypeCodex, Name: "d"},
	}
}

func TestPickTarget(t *testing.T) {
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	type tc struct {
		name      string
		mutate    func([]Instance)
		current   string
		tried     []string
		hops      int
		unhealthy []string          // health keys
		logins    map[string]string // instance name → shared login key
		refuse    []string          // tags the guard refuses
		ret       bool
		stop      bool // StopAtCurrent
		homeOnly  bool
		want      Target
		wantOK    bool
	}
	cases := []tc{
		{name: "home fails → first target", current: "claude/a", want: Target{Instance: "codex/b"}, wantOK: true},
		{name: "second hop follows home's chain, not the target's", current: "codex/b", tried: []string{"claude/a"}, hops: 1,
			want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "never back to home within the run, even when it looks healthy", current: "codex/b", tried: []string{"claude/a"}, hops: 1,
			want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "new run on a stand-in: a healthy home is a candidate again", current: "codex/b",
			want: Target{Instance: "claude/a", Home: true}, wantOK: true},
		{name: "same login as an instance tried earlier in the run skipped", current: "codex/b", tried: []string{"claude/a"}, hops: 1,
			logins: map[string]string{"a": "acct1", "c": "acct1"},
			want:   Target{Instance: "codex/d"}, wantOK: true},
		{name: "tried targets are skipped", current: "claude/c", tried: []string{"claude/a", "codex/b"}, hops: 2,
			want: Target{Instance: "codex/d"}, wantOK: true},
		{name: "disabled target skipped", current: "claude/a",
			mutate: func(all []Instance) { all[1].Disabled = true },
			want:   Target{Instance: "claude/c"}, wantOK: true},
		{name: "missing target skipped", current: "claude/a",
			mutate: func(all []Instance) { all[1].Name = "renamed" },
			want:   Target{Instance: "claude/c"}, wantOK: true},
		{name: "unhealthy target skipped", current: "claude/a", unhealthy: []string{"b"},
			want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "guard refusal skipped", current: "claude/a", refuse: []string{"codex/b"},
			want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "same login as the failed instance skipped", current: "claude/a", unhealthy: []string{"b"},
			logins: map[string]string{"a": "acct1", "c": "acct1"},
			want:   Target{Instance: "codex/d"}, wantOK: true},
		{name: "model pin not offered skipped", current: "claude/a",
			mutate: func(all []Instance) { all[0].Failover.Chain[0].ModelID = "nope" },
			want:   Target{Instance: "claude/c"}, wantOK: true},
		{name: "model pin offered is carried", current: "claude/a",
			mutate: func(all []Instance) { all[0].Failover.Chain[0].ModelID = "gpt-5" },
			want:   Target{Instance: "codex/b", ModelID: "gpt-5"}, wantOK: true},
		{name: "hop cap spent", current: "codex/d", tried: []string{"claude/a"}, hops: 3},
		{name: "hop cap follows a short chain", current: "codex/b", hops: 1,
			mutate: func(all []Instance) { all[0].Failover.Chain = all[0].Failover.Chain[:1] }},
		{name: "home failover off", current: "claude/a",
			mutate: func(all []Instance) { all[0].Failover.Enabled = false }},
		{name: "home instance gone", current: "codex/b",
			mutate: func(all []Instance) { all[0].Name = "gone" }},
		{name: "every target unusable", current: "claude/a", unhealthy: []string{"b", "c", "d"}},

		{name: "return: home healthy wins", ret: true, current: "codex/d", tried: []string{"claude/a", "codex/b"}, hops: 2,
			want: Target{Instance: "claude/a", Home: true}, wantOK: true},
		{name: "return: highest healthy above current", ret: true, current: "codex/d", unhealthy: []string{"a"},
			want: Target{Instance: "codex/b"}, wantOK: true},
		{name: "return: stay on current", ret: true, current: "claude/c", unhealthy: []string{"a", "b"},
			want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "return: current kept without asking the guard", ret: true, current: "claude/c", unhealthy: []string{"a", "b"},
			refuse: []string{"claude/c"}, want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "return: home's model pin rides along", ret: true, current: "codex/b",
			want: Target{Instance: "claude/a", ModelID: "", Home: true}, wantOK: true},
		{name: "return: home's return-home off", ret: true, current: "codex/b",
			mutate: func(all []Instance) { all[0].Failover.ReturnHome = false }},
		{name: "return: without stop-at-current a bad current moves down", ret: true, current: "codex/b",
			unhealthy: []string{"a", "b"}, want: Target{Instance: "claude/c"}, wantOK: true},
		{name: "return stop: a bad current never moves down", ret: true, stop: true, current: "codex/b",
			unhealthy: []string{"a", "b"}},
		{name: "return stop: moves up past a limited home", ret: true, stop: true, current: "codex/d",
			unhealthy: []string{"a"}, want: Target{Instance: "codex/b"}, wantOK: true},
		{name: "return stop: current off the chain, home only", ret: true, stop: true, current: "codex/zz",
			unhealthy: []string{"a"}},
		{name: "return stop: current off the chain, healthy home", ret: true, stop: true, current: "codex/zz",
			want: Target{Instance: "claude/a", Home: true}, wantOK: true},
		{name: "return stop: current is home", ret: true, stop: true, current: "claude/a"},
		{name: "return home-only: a healthy target above is not taken", ret: true, stop: true, homeOnly: true, current: "codex/d",
			unhealthy: []string{"a"}},
		{name: "return home-only: healthy home", ret: true, stop: true, homeOnly: true, current: "codex/d",
			want: Target{Instance: "claude/a", Home: true}, wantOK: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			all := failoverFixture()
			if c.mutate != nil {
				c.mutate(all)
			}
			unhealthy := map[string]bool{}
			for _, k := range c.unhealthy {
				unhealthy[k] = true
			}
			refuse := map[string]bool{}
			for _, k := range c.refuse {
				refuse[k] = true
			}
			got, ok := PickTarget(PickInput{
				HomeProvider:  "claude/a",
				Current:       c.current,
				Instances:     all,
				Tried:         c.tried,
				Hops:          c.hops,
				ReturnHome:    c.ret,
				StopAtCurrent: c.stop,
				HomeOnly:      c.homeOnly,
				Now:           now,
				Key: func(ins Instance) string {
					if k := c.logins[ins.Name]; k != "" {
						return k
					}
					return ins.Name
				},
				Healthy: func(key string, at time.Time) (bool, Health) {
					if !at.Equal(now) {
						t.Errorf("health asked at %v, want %v", at, now)
					}
					return !unhealthy[key], Health{}
				},
				Guard: func(tag string) error {
					if refuse[tag] {
						return errors.New("refused")
					}
					return nil
				},
			})
			if ok != c.wantOK || got != c.want {
				t.Fatalf("PickTarget = %+v, %v; want %+v, %v", got, ok, c.want, c.wantOK)
			}
		})
	}
}

// H2: the reader feeds PickTarget through RunFor, so a new message never
// inherits the spent hop cap or tried set of the previous one.
func TestPickTargetNewRunIgnoresOldRun(t *testing.T) {
	old := &session.FailoverState{HomeProvider: "claude/a", MsgKey: "m1", Tried: []string{"claude/a", "codex/b", "claude/c"}, Hops: 3}
	run := old.RunFor("m2", time.Now())
	got, ok := PickTarget(PickInput{
		HomeProvider: run.HomeProvider, Current: "claude/a", Instances: failoverFixture(),
		Tried: run.Tried, Hops: run.Hops,
		Healthy: func(string, time.Time) (bool, Health) { return true, Health{} },
		Key:     func(ins Instance) string { return ins.Name },
	})
	if !ok || got.Instance != "codex/b" {
		t.Fatalf("new run PickTarget = %+v, %v; want codex/b", got, ok)
	}
	same := old.RunFor("m1", time.Now())
	if _, ok := PickTarget(PickInput{HomeProvider: "claude/a", Current: "codex/d", Instances: failoverFixture(),
		Tried: same.Tried, Hops: same.Hops,
		Healthy: func(string, time.Time) (bool, Health) { return true, Health{} }}); ok {
		t.Fatal("the same run must keep its spent hop cap")
	}
}

func TestPickTargetReportsSkips(t *testing.T) {
	all := failoverFixture()
	all[1].Disabled = true
	var skipped [][2]string
	got, ok := PickTarget(PickInput{
		HomeProvider: "claude/a",
		Current:      "claude/a",
		Instances:    all,
		Tried:        []string{"claude/c"},
		Healthy:      func(string, time.Time) (bool, Health) { return true, Health{} },
		Key:          func(ins Instance) string { return ins.Name },
		OnSkip:       func(tag, reason string) { skipped = append(skipped, [2]string{tag, reason}) },
	})
	if !ok || got.Instance != "codex/d" {
		t.Fatalf("PickTarget = %+v, %v; want codex/d", got, ok)
	}
	want := [][2]string{{"claude/a", SkipCurrent}, {"codex/b", SkipDisabled}, {"claude/c", SkipTried}}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skips = %v, want %v", skipped, want)
	}
}

// Without injected funcs PickTarget reads the real registry: an account
// marked unhealthy there is skipped.
func TestPickTargetDefaultsToHealthRegistry(t *testing.T) {
	defer SetHealthDirForTest(t.TempDir())()
	all := failoverFixture()
	MarkUnhealthy(HealthKey(all[1]), Health{Kind: event.ErrLimit, Until: time.Now().Add(time.Hour)})
	got, ok := PickTarget(PickInput{HomeProvider: "claude/a", Current: "claude/a", Instances: all})
	if !ok || got.Instance != "claude/c" {
		t.Fatalf("PickTarget = %+v, %v; want claude/c", got, ok)
	}
}

func TestFailoverHelpers(t *testing.T) {
	if FailoverHopCap(1) != 1 || FailoverHopCap(5) != MaxFailoverTargets {
		t.Fatal("FailoverHopCap")
	}
	for k, want := range map[event.ErrKind]string{
		event.ErrLimit: FailoverTriggerLimit, event.ErrAuth: FailoverTriggerAuth,
		event.ErrUnavailable: FailoverTriggerUnavailable, event.ErrNone: "",
	} {
		if got := FailoverTrigger(k); got != want {
			t.Errorf("FailoverTrigger(%v) = %q, want %q", k, got, want)
		}
	}
	reset := time.Date(2026, 10, 13, 9, 40, 0, 0, time.FixedZone("WIB", 7*3600))
	if got := FailoverReason(event.ErrLimit, reset); got != "usage limit reached (resets 2026-10-13 02:40 UTC)" {
		t.Errorf("FailoverReason = %q", got)
	}
	if FailoverMsgKey("hi", "1") != FailoverMsgKey("hi", "1") || FailoverMsgKey("hi", "1") == FailoverMsgKey("ho", "1") || len(FailoverMsgKey("x", "")) != 16 {
		t.Error("FailoverMsgKey not a stable short hash")
	}
	// The same text sent twice is two runs.
	if FailoverMsgKey("ok", "turn-1") == FailoverMsgKey("ok", "turn-2") {
		t.Error("identical messages of two turns share a key")
	}
	if FailoverMsgKey("", "") != "" {
		t.Error("no message and no mark must give the empty key")
	}
}
