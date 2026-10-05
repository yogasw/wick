package slack

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	slackgo "github.com/slack-go/slack"
)

// Workflow Builder bots share their workflow's name with the app they mimic,
// so the picker must tell them apart and offer the app first.
func TestBotItemsMarksWorkflowBotsAndListsAppsFirst(t *testing.T) {
	users := []slackgo.User{
		{ID: "U_WF1", Name: "wf_bot_a0b8vjflq1l", RealName: "Wabster", IsBot: true},
		{ID: "U_APP", Name: "wabster", RealName: "Wabster", IsBot: true},
		{ID: "U_GONE", Name: "wabster_old", RealName: "Wabster", IsBot: true, Deleted: true},
		{ID: "U_HUMAN", Name: "wab", RealName: "Wab Human"},
		{ID: "U_OTHER", Name: "deploy", RealName: "deployment-waba", IsBot: true},
	}
	got := botItems(users, "wab")
	want := []string{"U_APP:Wabster", "U_OTHER:deployment-waba", "U_WF1:Wabster (workflow)"}
	if len(got) != len(want) {
		t.Fatalf("got %d items %+v, want %v", len(got), got, want)
	}
	for i, w := range want {
		if g := got[i].ID + ":" + got[i].Name; g != w {
			t.Errorf("item %d = %q, want %q", i, g, w)
		}
	}
}

// Typing fires one lookup per key; they must filter one shared listing, not
// page Slack each time — that burst is what hit "rate limited".
func TestCachedListingSharesOneFetch(t *testing.T) {
	key := t.Name()
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func() ([]string, error) {
		calls.Add(1)
		<-release
		return []string{"a"}, nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := cachedListing(key, fetch); err != nil || len(got) != 1 {
				t.Errorf("got %v err %v", got, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if _, err := cachedListing(key, fetch); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetches = %d, want 1", n)
	}
}

// An expired listing whose refresh fails keeps being served.
func TestCachedListingServesStaleOnError(t *testing.T) {
	key := t.Name()
	if _, err := cachedListing(key, func() ([]string, error) { return []string{"a"}, nil }); err != nil {
		t.Fatal(err)
	}
	listingMu.Lock()
	e := listingCache[key]
	e.at = e.at.Add(-listingTTL - time.Second)
	listingCache[key] = e
	listingMu.Unlock()
	got, err := cachedListing(key, func() ([]string, error) { return nil, errors.New("ratelimited") })
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v err %v", got, err)
	}
}
