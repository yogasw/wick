package agents

import (
	"net/http"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
)

// healthWorld is failoverWorld with an empty in-memory health registry
// and a fixed clock.
func healthWorld(t *testing.T, now time.Time) map[string]provider.Instance {
	t.Helper()
	store := failoverWorld(t)
	t.Cleanup(provider.SetHealthDirForTest(""))
	prev := healthNow
	t.Cleanup(func() { healthNow = prev })
	healthNow = func() time.Time { return now }
	return store
}

func TestProviderHealthDTO(t *testing.T) {
	now := time.Date(2026, 10, 13, 7, 0, 0, 0, time.UTC)
	store := healthWorld(t, now)
	mine := store["claude/mine"]

	if got := providerHealthDTO(mine); got != nil {
		t.Fatalf("healthy instance = %+v, want nil", got)
	}

	since := now.Add(-5 * time.Minute)
	until := now.Add(90 * time.Minute)
	provider.MarkInstanceUnhealthy(mine, provider.Health{Kind: event.ErrLimit, Since: since, Until: until, CheckedAt: since, Reason: "session limit"})
	got := providerHealthDTO(mine)
	want := ProviderHealthDTO{Kind: "limit", Since: since.Format(time.RFC3339), Until: until.Format(time.RFC3339), Reason: "session limit"}
	if got == nil || *got != want {
		t.Fatalf("limited instance = %+v, want %+v", got, want)
	}

	// Past its reset time the limit no longer counts: no badge.
	healthNow = func() time.Time { return until.Add(time.Minute) }
	if got := providerHealthDTO(mine); got != nil {
		t.Fatalf("lapsed limit = %+v, want nil", got)
	}
	healthNow = func() time.Time { return now }

	// A broken login: unhealthy until its retry window opens, then shown
	// as retrying rather than dropped.
	sib := store["codex/waba"] // another login: claude/* may share mine's
	failed := now.Add(-20 * time.Minute)
	provider.MarkInstanceUnhealthy(sib, provider.Health{Kind: event.ErrAuth, Since: failed, CheckedAt: failed, FailedAt: failed, RetryAt: now.Add(10 * time.Minute)})
	if got := providerHealthDTO(sib); got == nil || got.Kind != "auth" || got.Retrying {
		t.Fatalf("auth before retry window = %+v", got)
	}
	healthNow = func() time.Time { return now.Add(11 * time.Minute) }
	if got := providerHealthDTO(sib); got == nil || got.Kind != "auth" || !got.Retrying {
		t.Fatalf("auth in open retry window = %+v, want retrying", got)
	}
	healthNow = func() time.Time { return now }

	// Both payloads carry it: the list row and the detail page.
	row := providerStatusDTO(provider.Status{Instance: mine}, nil)
	if row.Instance.Health == nil || row.Instance.Health.Kind != "limit" {
		t.Fatalf("list row health = %+v", row.Instance.Health)
	}
}

func TestResetProviderHealth(t *testing.T) {
	now := time.Now()
	store := healthWorld(t, now)
	key := provider.HealthKey(store["claude/mine"])
	mark := func() {
		provider.MarkUnhealthy(key, provider.Health{Kind: event.ErrAuth, Since: now, CheckedAt: now})
	}
	mark()

	// Not the owner: refused, the record stays.
	w, c := userCtx(t, failoverOther, "/", map[string]string{"type": "claude", "name": "mine"})
	resetProviderHealth(c)
	if w.Code == http.StatusOK {
		t.Fatalf("other user reset: status=%d", w.Code)
	}
	if _, ok := provider.HealthOf(key); !ok {
		t.Fatal("refused reset cleared the record")
	}

	// Owner whose failover permission is off: refused too.
	lockedKey := provider.HealthKey(store["claude/locked"])
	provider.MarkUnhealthy(lockedKey, provider.Health{Kind: event.ErrAuth, Since: now, CheckedAt: now})
	w, c = userCtx(t, failoverOwner, "/", map[string]string{"type": "claude", "name": "locked"})
	resetProviderHealth(c)
	if w.Code == http.StatusOK {
		t.Fatalf("locked owner reset: status=%d", w.Code)
	}
	if _, ok := provider.HealthOf(lockedKey); !ok {
		t.Fatal("refused reset cleared the locked record")
	}

	for _, u := range []struct {
		name string
		ctx  func() (int, bool)
	}{
		{"owner", func() (int, bool) {
			w, c := userCtx(t, failoverOwner, "/", map[string]string{"type": "claude", "name": "mine"})
			resetProviderHealth(c)
			_, ok := provider.HealthOf(key)
			return w.Code, ok
		}},
		{"admin", func() (int, bool) {
			w, c := userCtx(t, failoverAdmin, "/", map[string]string{"type": "claude", "name": "mine"})
			resetProviderHealth(c)
			_, ok := provider.HealthOf(key)
			return w.Code, ok
		}},
	} {
		mark()
		if code, still := u.ctx(); code != http.StatusOK || still {
			t.Fatalf("%s reset: status=%d recordLeft=%v", u.name, code, still)
		}
	}

	w, c = userCtx(t, failoverAdmin, "/", map[string]string{"type": "claude", "name": "gone"})
	resetProviderHealth(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown instance: status=%d", w.Code)
	}
}
