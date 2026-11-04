package custom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// rotatingTokenServer mimics an authorization server that rotates refresh
// tokens: rt-N is good once and yields rt-(N+1); a spent one is
// invalid_grant.
func rotatingTokenServer(t *testing.T) (url string, grants *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	current, n := "rt-0", 0
	grants = &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		grants.Add(1)
		if r.Form.Get("refresh_token") != current {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid refresh token"}`))
			return
		}
		n++
		current = fmt.Sprintf("rt-%d", n)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("at-%d", n), "refresh_token": current, "expires_in": 3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, grants
}

// Concurrent calls on an expired instance token refresh once; the rest
// reuse the renewed token instead of spending the rotated refresh token.
func TestInstanceRefreshConcurrentRotatingToken(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)
	tokenURL, grants := rotatingTokenServer(t)
	meta := &oauthClientMeta{TokenEndpoint: tokenURL, ClientID: "cid"}
	setInstanceToken(svc, instanceID, "at-old", "rt-0", time.Now().Add(-time.Hour).Format(time.RFC3339))

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := svc.instanceAccessToken(context.Background(), meta, instanceID)
			if err == nil && tok != "at-1" {
				err = fmt.Errorf("token %q, want at-1", tok)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if g := grants.Load(); g != 1 {
		t.Errorf("refresh grants = %d, want 1", g)
	}
}

// A refresh token rotated behind our back (another process) is re-read
// and retried once rather than reported as an auth failure.
func TestInstanceRefreshRetriesWithRotatedToken(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)
	tokenURL, _ := rotatingTokenServer(t)
	meta := &oauthClientMeta{TokenEndpoint: tokenURL, ClientID: "cid"}
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	// Another process already spent rt-0 and stored rt-1, but the stored
	// value flips only after our first grant fails.
	setInstanceToken(svc, instanceID, "at-old", "rt-0", past)
	if _, err := svc.refreshTokens(context.Background(), meta, "rt-0"); err != nil {
		t.Fatal(err)
	}
	keys := svc.keys.(*fakeKeyStore)
	svc.keys = &flipOnSecondRead{fakeKeyStore: keys, owner: "connector:" + instanceID, next: "rt-1"}

	tok, err := svc.instanceAccessToken(context.Background(), meta, instanceID)
	if err != nil {
		t.Fatalf("refresh after rotation: %v", err)
	}
	if tok != "at-2" {
		t.Errorf("token %q, want at-2", tok)
	}
}

// flipOnSecondRead serves the stale refresh token once, then the rotated
// one — a write that landed from elsewhere between the two reads.
type flipOnSecondRead struct {
	*fakeKeyStore
	owner, next string
	reads       int
}

func (f *flipOnSecondRead) GetOwned(owner, key string) string {
	if owner == f.owner && key == cfgOAuthRefresh {
		f.reads++
		if f.reads > 1 {
			return f.next
		}
	}
	return f.fakeKeyStore.GetOwned(owner, key)
}

// A refused refresh is stored on the instance so the row stays flagged
// after a reload; the next working token clears it.
func TestInstanceAuthFailureIsRecorded(t *testing.T) {
	svc, instanceID, _ := newProbeFixture(t)
	tokenURL, _ := rotatingTokenServer(t)
	meta := &oauthClientMeta{TokenEndpoint: tokenURL, ClientID: "cid"}
	owner := "connector:" + instanceID
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)

	setInstanceToken(svc, instanceID, "at-old", "rt-dead", past)
	if _, err := svc.instanceAccessToken(context.Background(), meta, instanceID); err == nil {
		t.Fatal("a dead refresh token must fail")
	}
	if got := svc.keys.GetOwned(owner, cfgOAuthError); got == "" {
		t.Fatal("auth failure was not recorded on the instance")
	}

	setInstanceToken(svc, instanceID, "at-old", "rt-0", past)
	if _, err := svc.instanceAccessToken(context.Background(), meta, instanceID); err != nil {
		t.Fatalf("refresh with a live token: %v", err)
	}
	if got := svc.keys.GetOwned(owner, cfgOAuthError); got != "" {
		t.Errorf("auth failure %q not cleared after a working refresh", got)
	}
}
