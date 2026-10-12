package provider

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

func healthTestDirs(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(SetHealthDirForTest(dir))
	t.Cleanup(SetUsageDiskDirForTest(dir + "/usage"))
	return dir
}

func TestUsageDiskOffUnderTest(t *testing.T) {
	if usageDiskDir() != "" || healthDir() != "" {
		t.Fatal("persistence must be off under go test unless a test points it somewhere")
	}
	UsageDiskStore(UsageDisk{Key: "claude:x", ObservedAt: time.Now()})
	if _, ok := UsageDiskLookup("claude:x"); ok {
		t.Fatal("lookup hit with persistence off")
	}
}

func TestUsageDiskRoundTrip(t *testing.T) {
	healthTestDirs(t)
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	in := UsageDisk{Key: "claude:a@b", ObservedAt: at, Windows: []UsageDiskWindow{
		{Key: "five_hour", Utilization: 42, ResetsAt: at.Add(time.Hour)},
	}}
	UsageDiskStore(in)
	got, ok := UsageDiskLookup("claude:a@b")
	if !ok || !got.ObservedAt.Equal(at) || len(got.Windows) != 1 || got.Windows[0].Utilization != 42 {
		t.Fatalf("round trip = %+v, %v", got, ok)
	}
	UsageDiskDrop("claude:a@b")
	if _, ok := UsageDiskLookup("claude:a@b"); ok {
		t.Fatal("dropped entry still read")
	}
}

func TestUsageDiskKeyMismatchIgnored(t *testing.T) {
	healthTestDirs(t)
	b, _ := json.Marshal(UsageDisk{Key: "claude:someone-else", ObservedAt: time.Now()})
	path := usageDiskFile("claude:me")
	writeFileAtomic(path, b)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, ok := UsageDiskLookup("claude:me"); ok {
		t.Fatal("a file holding another account's key must be ignored")
	}
}

func TestUsageDiskVerdict(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	since := now.Add(-time.Hour)
	read := now.Add(-time.Minute)
	type v struct{ recovered, known bool }
	cases := []struct {
		name string
		ws   []UsageDiskWindow
		want v
	}{
		// M2: nothing real in the reading → no say.
		{"empty", nil, v{false, false}},
		{"placeholder only", []UsageDiskWindow{{Error: "boom"}}, v{false, false}},
		{"all under", []UsageDiskWindow{{Key: "a", Utilization: 99}, {Key: "b", Utilization: 10}}, v{true, true}},
		{"one full", []UsageDiskWindow{{Key: "a", Utilization: 100, ResetsAt: now.Add(time.Hour)}, {Key: "b", Utilization: 1}}, v{false, true}},
		{"full but reset passed", []UsageDiskWindow{{Key: "a", Utilization: 100, ResetsAt: now.Add(-time.Minute)}}, v{true, true}},
		// M1: a codex window reported by a turn BEFORE the limit proves
		// nothing, however recently the file was read.
		{"codex snapshot before limit", []UsageDiskWindow{{Key: "a", Utilization: 50, ObservedAt: since.Add(-time.Minute)}}, v{false, false}},
		{"codex snapshot after limit", []UsageDiskWindow{{Key: "a", Utilization: 50, ObservedAt: since.Add(time.Minute)}}, v{true, true}},
		// M3: a pool recovers when one account does.
		{"pool: one account clear", []UsageDiskWindow{
			{Key: "a", Account: "x", Utilization: 100, ResetsAt: now.Add(time.Hour)},
			{Key: "a", Account: "y", Utilization: 20},
		}, v{true, true}},
		{"pool: all accounts full", []UsageDiskWindow{
			{Key: "a", Account: "x", Utilization: 100, ResetsAt: now.Add(time.Hour)},
			{Key: "a", Account: "y", Utilization: 100},
		}, v{false, true}},
		{"pool: full + unknown", []UsageDiskWindow{
			{Key: "a", Account: "x", Utilization: 100},
			{Key: "a", Account: "y", Utilization: 20, ObservedAt: since.Add(-time.Hour)},
		}, v{false, false}},
	}
	for _, c := range cases {
		r, k := (UsageDisk{ObservedAt: read, Windows: c.ws}).Verdict(now, since)
		if (v{r, k}) != c.want {
			t.Errorf("%s: Verdict = %v/%v, want %+v", c.name, r, k, c.want)
		}
	}
}

func TestUsageDiskFailureOnlyEntry(t *testing.T) {
	healthTestDirs(t)
	at := time.Now()
	UsageDiskStore(UsageDisk{Key: "k", AttemptedAt: at, Fails: 3, ServerUntil: at.Add(time.Hour)})
	d, ok := UsageDiskLookup("k")
	if !ok || d.Fails != 3 || !d.ServerUntil.Equal(at.Add(time.Hour)) || !d.ObservedAt.IsZero() {
		t.Fatalf("failure bookkeeping not persisted: %+v, %v", d, ok)
	}
	if usageFresh(d, at) {
		t.Fatal("an entry with no good reading must never count as fresh")
	}
}

func TestCorruptFilesIgnored(t *testing.T) {
	dir := healthTestDirs(t)
	writeFileAtomic(usageDiskFile("k"), []byte("{not json"))
	if _, ok := UsageDiskLookup("k"); ok {
		t.Fatal("corrupt usage file read")
	}
	writeFileAtomic(dir+"/health.json", []byte("null"))
	SetHealthDirForTest(dir)
	if len(UnhealthyAccounts()) != 0 {
		t.Fatal("null health.json produced entries")
	}
}

func TestMarkUnhealthyIdempotentAndPersisted(t *testing.T) {
	dir := healthTestDirs(t)
	t0 := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: t0, CheckedAt: t0, Until: t0.Add(2 * time.Hour), Reason: "session limit"})
	// The same error again, later and with an earlier reset: Since stays,
	// Until never shrinks.
	MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: t0.Add(time.Minute), CheckedAt: t0.Add(time.Minute), Until: t0.Add(time.Hour)})
	h, ok := HealthOf("k")
	if !ok || !h.Since.Equal(t0) || !h.Until.Equal(t0.Add(2*time.Hour)) || !h.CheckedAt.Equal(t0.Add(time.Minute)) || h.Reason != "session limit" {
		t.Fatalf("after repeat = %+v", h)
	}
	MarkUnhealthy("", Health{Kind: event.ErrLimit})
	MarkUnhealthy("none", Health{Kind: event.ErrNone})
	if len(UnhealthyAccounts()) != 1 {
		t.Fatalf("empty key / ErrNone must not be recorded: %+v", UnhealthyAccounts())
	}

	// A restart reads health.json back.
	restore := SetHealthDirForTest(dir)
	defer restore()
	if h2, ok := HealthOf("k"); !ok || !h2.Until.Equal(h.Until) {
		t.Fatalf("not persisted: %+v, %v", h2, ok)
	}
	TouchHealth("k", t0.Add(30*time.Minute))
	if h3, _ := HealthOf("k"); !h3.CheckedAt.Equal(t0.Add(30 * time.Minute)) {
		t.Fatalf("touch = %+v", h3)
	}
	// L2: a clear decided about an older failure leaves a newer one.
	if MarkHealthyIf("k", t0.Add(-time.Second)) {
		t.Fatal("MarkHealthyIf erased a failure newer than its decision")
	}
	if !MarkHealthyIf("k", t0) {
		t.Fatal("MarkHealthyIf did not clear the failure it decided about")
	}
	SetHealthDirForTest(dir)
	if _, ok := HealthOf("k"); ok {
		t.Fatal("healthy account still on disk")
	}
}

func TestHealthKeyFallback(t *testing.T) {
	prev := HealthKeyFunc
	defer func() { HealthKeyFunc = prev }()
	HealthKeyFunc = nil
	if got := HealthKey(Instance{Type: TypeGemini, Name: "g1"}); got != "gemini/g1" {
		t.Fatalf("fallback = %q", got)
	}
	HealthKeyFunc = func(Instance) string { return "claude:me" }
	if got := HealthKey(Instance{Type: TypeClaude, Name: "a"}); got != "claude:me" {
		t.Fatalf("hook = %q", got)
	}
}

func TestHealthyNow(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	marked := now.Add(-30 * time.Minute)
	limit := Health{Kind: event.ErrLimit, Since: marked, CheckedAt: marked, Until: now.Add(3 * time.Hour)}
	under := []UsageDiskWindow{{Key: "five_hour", Utilization: 12}}
	full := []UsageDiskWindow{{Key: "five_hour", Utilization: 100, ResetsAt: now.Add(3 * time.Hour)}}

	cases := []struct {
		name  string
		h     *Health
		usage *UsageDisk
		want  bool
	}{
		{"nothing recorded", nil, nil, true},
		{"limit, no reading", &limit, nil, false},
		{"manual reset: fresh reading clears before Until", &limit, &UsageDisk{ObservedAt: now.Add(-time.Minute), Windows: under}, true},
		{"stale reading not trusted", &limit, &UsageDisk{ObservedAt: now.Add(-UsageFreshFor - time.Minute), Windows: under}, false},
		{"reading older than the limit not trusted", &limit, &UsageDisk{ObservedAt: marked.Add(-time.Minute), Windows: under}, false},
		{"fresh reading still full", &limit, &UsageDisk{ObservedAt: now.Add(-time.Minute), Windows: full}, false},
		{"Until passed", &Health{Kind: event.ErrLimit, Since: marked, Until: now.Add(-time.Minute)}, nil, true},
		{"Until passed but fresh reading full", &Health{Kind: event.ErrLimit, Since: marked, Until: now.Add(-time.Minute)}, &UsageDisk{ObservedAt: now.Add(-time.Minute), Windows: full}, false},
		// H1: no Until → one recheck interval after Since.
		{"no Until, before Since+Recheck", &Health{Kind: event.ErrLimit, Since: marked, Recheck: time.Hour}, nil, false},
		{"no Until, past Since+Recheck", &Health{Kind: event.ErrLimit, Since: marked, Recheck: 20 * time.Minute}, nil, true},
		{"no Until, default recheck", &Health{Kind: event.ErrLimit, Since: marked}, nil, true},
		// M2: a fresh reading with no real window has no vote.
		{"Until passed, fresh empty reading", &Health{Kind: event.ErrLimit, Since: marked, Until: now.Add(-time.Minute)}, &UsageDisk{ObservedAt: now.Add(-time.Minute), Windows: []UsageDiskWindow{{Error: "all accounts failed"}}}, true},
		{"auth ignores usage", &Health{Kind: event.ErrAuth, Since: marked}, &UsageDisk{ObservedAt: now.Add(-time.Minute), Windows: under}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			healthTestDirs(t)
			if c.h != nil {
				MarkUnhealthy("k", *c.h)
			}
			if c.usage != nil {
				u := *c.usage
				u.Key = "k"
				UsageDiskStore(u)
			}
			if got, _ := HealthyNow("k", now); got != c.want {
				t.Fatalf("HealthyNow = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRecordUsageClearsLimit(t *testing.T) {
	healthTestDirs(t)
	now := time.Now()
	MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: now.Add(-time.Hour), Until: now.Add(time.Hour)})
	RecordUsage(UsageDisk{Key: "k", ObservedAt: now, Windows: []UsageDiskWindow{{Key: "w", Utilization: 100, ResetsAt: now.Add(time.Hour)}}})
	if _, ok := HealthOf("k"); !ok {
		t.Fatal("a full reading must not clear the limit")
	}
	RecordUsage(UsageDisk{Key: "k", ObservedAt: now, Windows: []UsageDiskWindow{{Key: "w", Utilization: 3}}})
	if _, ok := HealthOf("k"); ok {
		t.Fatal("a reading under 100% after the limit must clear it")
	}
	if _, ok := UsageDiskLookup("k"); !ok {
		t.Fatal("RecordUsage must write the file")
	}
}

func TestRequestRefresh(t *testing.T) {
	healthTestDirs(t)
	called := make(chan string, 4)
	SetUsageRefreshHook(func(key string) { called <- key })
	defer SetUsageRefreshHook(nil)

	if RequestRefresh("k") {
		t.Fatal("healthy account must not refresh")
	}
	MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: time.Now().Add(-time.Hour)})
	if !RequestRefresh("k") {
		t.Fatal("limited account with no reading must refresh")
	}
	select {
	case k := <-called:
		if k != "k" {
			t.Fatalf("hook key = %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook not called")
	}
	UsageDiskStore(UsageDisk{Key: "k", ObservedAt: time.Now(), Windows: []UsageDiskWindow{{Key: "w", Utilization: 100}}})
	if RequestRefresh("k") {
		t.Fatal("a fresh reading needs no refresh")
	}
	MarkUnhealthy("a", Health{Kind: event.ErrAuth})
	if RequestRefresh("a") {
		t.Fatal("auth failures are not a usage question")
	}
}

// TestLimitBackAfterUsageClearIsTimeOnly (R5): a limit that fails again
// right after a usage reading said it cleared comes from a window usage
// does not report, so the next wait is time only.
func TestLimitBackAfterUsageClearIsTimeOnly(t *testing.T) {
	under := []UsageDiskWindow{{Key: "five_hour", Utilization: 12}}

	t.Run("fresh reading cleared the entry still on record", func(t *testing.T) {
		healthTestDirs(t)
		now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
		marked := now.Add(-30 * time.Minute)
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: marked, CheckedAt: marked, Until: now.Add(time.Hour)})
		UsageDiskStore(UsageDisk{Key: "k", ObservedAt: now.Add(-time.Minute), Windows: under})
		if ok, _ := HealthyNow("k", now); !ok {
			t.Fatal("fresh reading under 100% must clear before Until")
		}
		// The turn it let through hits the limit again.
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, CheckedAt: now, Recheck: 10 * time.Minute})
		h, _ := HealthOf("k")
		if !h.TimeOnly || !h.Since.Equal(now) {
			t.Fatalf("want TimeOnly with Since=%v, got %+v", now, h)
		}
		if ok, _ := HealthyNow("k", now.Add(time.Minute)); ok {
			t.Fatal("a TimeOnly limit must ignore the fresh reading")
		}
		if ok, _ := HealthyNow("k", now.Add(61*time.Minute)); !ok {
			t.Fatal("past the kept Until the limit lifts by time")
		}
		// A repeat keeps it TimeOnly.
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, CheckedAt: now.Add(2 * time.Minute)})
		if h, _ := HealthOf("k"); !h.TimeOnly || !h.Since.Equal(now) {
			t.Fatalf("repeat must keep TimeOnly and Since, got %+v", h)
		}
	})

	t.Run("cleared by a reading within the recheck interval", func(t *testing.T) {
		healthTestDirs(t)
		now := time.Now()
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: now.Add(-time.Hour), Until: now.Add(time.Hour)})
		RecordUsage(UsageDisk{Key: "k", ObservedAt: now, Windows: under})
		if _, bad := HealthOf("k"); bad {
			t.Fatal("reading under 100% must clear")
		}
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, CheckedAt: now.Add(time.Minute), Recheck: 10 * time.Minute})
		h, _ := HealthOf("k")
		if !h.TimeOnly {
			t.Fatalf("limit back within recheck must be TimeOnly, got %+v", h)
		}
		RecordUsage(UsageDisk{Key: "k", ObservedAt: now.Add(2 * time.Minute), Windows: under})
		if _, bad := HealthOf("k"); !bad {
			t.Fatal("a usage reading must not clear a TimeOnly limit")
		}
		MarkHealthyIf("k", h.Since)
		if _, bad := HealthOf("k"); bad {
			t.Fatal("time-based clear must still work")
		}
		// The next failure, past the recheck after that clear, is normal.
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, CheckedAt: now.Add(30 * time.Minute)})
		if h, _ := HealthOf("k"); h.TimeOnly {
			t.Fatalf("a failure after a time-based clear is not TimeOnly: %+v", h)
		}
	})

	t.Run("back after the recheck interval is a normal limit", func(t *testing.T) {
		healthTestDirs(t)
		now := time.Now()
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, Since: now.Add(-time.Hour)})
		RecordUsage(UsageDisk{Key: "k", ObservedAt: now, Windows: under})
		MarkUnhealthy("k", Health{Kind: event.ErrLimit, CheckedAt: now.Add(11 * time.Minute), Recheck: 10 * time.Minute})
		if h, _ := HealthOf("k"); h.TimeOnly {
			t.Fatalf("want a normal limit, got %+v", h)
		}
	})
}
