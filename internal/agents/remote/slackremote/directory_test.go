package slackremote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/remote"
)

// fakeSlackDirectory serves users.list in two cursor pages and
// conversations.list in one; missing makes every call fail missing_scope.
func fakeSlackDirectory(t *testing.T, missing bool) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if missing {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "missing_scope", "needed": "users:read"})
			return
		}
		switch r.URL.Path {
		case "/users.list":
			if r.Form.Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true,
					"members": []map[string]any{
						{"id": "U1", "name": "alpha.tester", "profile": map[string]any{"real_name": "Alpha Tester", "display_name": "alpha"}},
						{"id": "U2", "name": "gone.user", "deleted": true, "profile": map[string]any{"real_name": "Alpha Gone"}},
					},
					"response_metadata": map[string]any{"next_cursor": "page2"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true,
				"members": []map[string]any{
					{"id": "B1", "name": "helper-bot", "is_bot": true, "profile": map[string]any{"real_name": "Helper Bot"}},
					{"id": "U3", "name": "beta.tester", "profile": map[string]any{"real_name": "Beta Tester"}},
				},
				"response_metadata": map[string]any{"next_cursor": ""}})
		case "/conversations.list":
			if r.Form.Get("exclude_archived") != "true" || !strings.Contains(r.Form.Get("types"), "private_channel") {
				t.Errorf("conversations.list form = %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true,
				"channels": []map[string]any{
					{"id": "C1", "name": "ops-alerts"},
					{"id": "C2", "name": "ops-secret", "is_private": true},
					{"id": "C3", "name": "ops-old", "is_archived": true},
				}})
		default:
			t.Errorf("unexpected call %s", r.URL.Path)
		}
	}))
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old; srv.Close() })
	return &calls
}

func ids(es []DirEntry) string {
	var s []string
	for _, e := range es {
		s = append(s, e.ID)
	}
	return strings.Join(s, ",")
}

// Users come from every cursor page, deleted ones dropped, matched on any
// name case-insensitively; bots are flagged.
func TestDirectorySearchUsersPagesAndFilters(t *testing.T) {
	calls := fakeSlackDirectory(t, false)
	d := NewDirectory()
	api := HTTPAPI{Token: "x"}
	got, err := d.Search(context.Background(), api, "ws", DirUsers, "TESTER")
	if err != nil || ids(got) != "U1,U3" {
		t.Fatalf("tester: %v %v", ids(got), err)
	}
	got, _ = d.Search(context.Background(), api, "ws", DirUsers, "@helper")
	if ids(got) != "B1" || !got[0].IsBot {
		t.Fatalf("bot: %+v", got)
	}
	if got, _ = d.Search(context.Background(), api, "ws", DirUsers, "gone"); len(got) != 0 {
		t.Fatalf("deleted user listed: %+v", got)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("users.list called %d times, want 2 (two pages, then cached)", n)
	}
}

// The last response answers every search; after DirectoryRefresh one search
// refreshes it in the background without making the caller wait.
func TestDirectoryRefreshesInBackground(t *testing.T) {
	calls := fakeSlackDirectory(t, false)
	d := NewDirectory()
	var mu sync.Mutex
	now := time.Now()
	d.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	api := HTTPAPI{Token: "x"}
	for _, q := range []string{"o", "op", "ops"} {
		if _, err := d.Search(context.Background(), api, "ws", DirChannels, q); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	mu.Lock()
	now = now.Add(DirectoryRefresh + time.Second)
	mu.Unlock()
	got, _ := d.Search(context.Background(), api, "ws", DirChannels, "ops")
	if ids(got) != "C1,C2" || !got[1].IsPrivate {
		t.Fatalf("channels: %+v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("calls after refresh = %d, want 2", calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// At most DirectoryMaxResults come back.
func TestDirectoryLimit(t *testing.T) {
	d := NewDirectory()
	var many []DirEntry
	for i := 0; i < 50; i++ {
		many = append(many, DirEntry{ID: "U", Name: "user"})
	}
	got, err := d.Search(context.Background(), fixedDir{users: many}, "ws", DirUsers, "user")
	if err != nil || len(got) != DirectoryMaxResults {
		t.Fatalf("len %d err %v", len(got), err)
	}
}

// missing_scope names the scope to add.
func TestDirectoryMissingScope(t *testing.T) {
	fakeSlackDirectory(t, true)
	d := NewDirectory()
	_, err := d.Search(context.Background(), HTTPAPI{Token: "x"}, "ws", DirUsers, "a")
	var ms *MissingScopeError
	if !errors.As(err, &ms) || !strings.Contains(err.Error(), "users:read") || !strings.Contains(err.Error(), "manually") {
		t.Fatalf("users: %v", err)
	}
	_, err = d.Search(context.Background(), HTTPAPI{Token: "x"}, "ws", DirChannels, "a")
	if !errors.As(err, &ms) || !strings.Contains(err.Error(), "groups:read") {
		t.Fatalf("channels: %v", err)
	}
	if _, err := d.Search(context.Background(), HTTPAPI{Token: "x"}, "ws", "files", "a"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

type fixedDir struct{ users, channels []DirEntry }

func (f fixedDir) ListUsers(context.Context) ([]DirEntry, error)    { return f.users, nil }
func (f fixedDir) ListChannels(context.Context) ([]DirEntry, error) { return f.channels, nil }

// slowDir counts listings and blocks each one until release is closed.
type slowDir struct {
	calls   atomic.Int32
	release chan struct{}
	mu      sync.Mutex
	err     error
}

func (s *slowDir) setErr(err error) { s.mu.Lock(); s.err = err; s.mu.Unlock() }

func (s *slowDir) ListUsers(ctx context.Context) ([]DirEntry, error) {
	s.calls.Add(1)
	<-s.release
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return []DirEntry{{ID: "U1", Name: "alpha"}}, nil
}
func (s *slowDir) ListChannels(ctx context.Context) ([]DirEntry, error) { return s.ListUsers(ctx) }

// Searches typed while the first listing is still paging share it instead
// of each paging the workspace again — the burst that tripped the rate limit.
func TestDirectoryConcurrentSearchesShareOneListing(t *testing.T) {
	d := NewDirectory()
	api := &slowDir{release: make(chan struct{})}
	errs := make(chan error, 5)
	for _, q := range []string{"a", "al", "alp", "alph", "alpha"} {
		go func(q string) {
			got, err := d.Search(context.Background(), api, "ws", DirChannels, q)
			if err == nil && ids(got) != "U1" {
				err = errors.New("got " + ids(got))
			}
			errs <- err
		}(q)
	}
	time.Sleep(50 * time.Millisecond)
	close(api.release)
	for i := 0; i < 5; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if n := api.calls.Load(); n != 1 {
		t.Fatalf("listings = %d, want 1", n)
	}
}

// Once a listing has answered, a failing refresh (rate limited) never
// surfaces, and no further refresh is tried while Retry-After runs.
func TestDirectoryServesLastResponseOnError(t *testing.T) {
	d := NewDirectory()
	var mu sync.Mutex
	now := time.Now()
	d.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	api := &slowDir{release: make(chan struct{})}
	close(api.release)
	if _, err := d.Search(context.Background(), api, "ws", DirUsers, "al"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = now.Add(DirectoryRefresh + time.Second)
	mu.Unlock()
	api.setErr(&remote.RetryAfterError{After: time.Hour, Err: errors.New("slack users.list: rate limited")})
	for i := 0; i < 5; i++ {
		got, err := d.Search(context.Background(), api, "ws", DirUsers, "al")
		if err != nil || ids(got) != "U1" {
			t.Fatalf("search %d: %v %v", i, ids(got), err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := api.calls.Load(); n != 2 {
		t.Fatalf("listings = %d, want 2 (first + one refresh, then cooldown)", n)
	}
}

// A 429 page is retried after Retry-After instead of failing the listing.
func TestDirectoryRetriesRateLimitedPage(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "channels": []map[string]any{{"id": "C1", "name": "support"}}})
	}))
	old := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = old; srv.Close() })
	got, err := NewDirectory().Search(context.Background(), HTTPAPI{Token: "x"}, "ws", DirChannels, "sup")
	if err != nil || ids(got) != "C1" || calls.Load() != 2 {
		t.Fatalf("got %v err %v calls %d", ids(got), err, calls.Load())
	}
}
